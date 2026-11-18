// Package logging sets up zerolog for the whole process: level, console format, filter_components and the extra
// outputs of the agent (logs service, syslog).
package logging

import (
	"io"
	"os"
	"sync/atomic"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/grpclog"
)

type Options struct {
	// trace, debug, info, warn (or warning), error, fatal, panic; empty means info
	Level            string
	NoColor          bool
	ForceColors      bool
	InJson           bool
	FilterComponents []string
	// nil means os.Stderr
	Console io.Writer
	// optional, receives the lines kept by FilterComponents as colored text, one Write per line
	Listener io.Writer
	// optional, receives every JSON line, FilterComponents does not apply, one Write per line
	Syslog io.Writer
}

// root is the writer of every logger. Setup only changes where it writes to, so loggers derived before Setup,
// package variables included, follow the configuration.
var root = &swappableWriter{}

type swappableWriter struct {
	dest atomic.Pointer[io.Writer]
}

func (w *swappableWriter) Write(p []byte) (int, error) {
	return (*w.dest.Load()).Write(p)
}

func init() {
	log.Logger = zerolog.New(root).With().Timestamp().Logger()
	_ = Setup(Options{}) // cannot fail without level and filters
	// grpc requires its logger to be set before any gRPC call, init runs before any of them
	grpclog.SetLoggerV2(newGrpcLogger(Component("grpc")))
}

// Setup applies the log configuration to every logger of the process
func Setup(opts Options) error {
	level, err := ParseLevel(opts.Level)
	if err != nil {
		return err
	}
	console := opts.Console
	if console == nil {
		console = os.Stderr
	}

	filteredOutputs := []io.Writer{consoleOutput(console, opts)}
	if opts.Listener != nil {
		// the logs service always gives colored text, whatever the console format is
		filteredOutputs = append(filteredOutputs, zerolog.ConsoleWriter{Out: opts.Listener, TimeFormat: time.RFC3339})
	}
	filter, err := newComponentFilter(opts.FilterComponents, zerolog.MultiLevelWriter(filteredOutputs...))
	if err != nil {
		return err
	}
	outputs := []io.Writer{filter}
	if opts.Syslog != nil {
		outputs = append(outputs, opts.Syslog)
	}

	var dest io.Writer = zerolog.SyncWriter(zerolog.MultiLevelWriter(outputs...))
	root.dest.Store(&dest)
	zerolog.SetGlobalLevel(level)
	return nil
}

func consoleOutput(console io.Writer, opts Options) io.Writer {
	if opts.InJson {
		return console
	}
	colored := (opts.ForceColors || isTerminal(console)) && !opts.NoColor
	return zerolog.ConsoleWriter{Out: console, NoColor: !colored, TimeFormat: time.RFC3339}
}

func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	return ok && isatty.IsTerminal(file.Fd())
}

// Component gives a logger whose lines carry component=name, the key of filter_components
func Component(name string) zerolog.Logger {
	return log.Logger.With().Str("component", name).Logger()
}

// ParseLevel reads a configured level, empty means info and "warning" is accepted as logrus did
func ParseLevel(level string) (zerolog.Level, error) {
	switch level {
	case "":
		return zerolog.InfoLevel, nil
	case "warning":
		return zerolog.WarnLevel, nil
	}
	return zerolog.ParseLevel(level)
}
