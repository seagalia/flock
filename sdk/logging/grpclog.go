package logging

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/grpclog"
)

// grpcLogger sends grpc-go internal logs to flock logging. grpc logs a lot at info (connection states, resolver
// updates), these lines are flock's debug.
type grpcLogger struct {
	logger zerolog.Logger
}

func newGrpcLogger(logger zerolog.Logger) grpclog.LoggerV2 {
	return &grpcLogger{logger: logger}
}

func sprintln(args []any) string {
	return strings.TrimSuffix(fmt.Sprintln(args...), "\n")
}

func (g *grpcLogger) Info(args ...any)                    { g.logger.Debug().Msg(fmt.Sprint(args...)) }
func (g *grpcLogger) Infoln(args ...any)                  { g.logger.Debug().Msg(sprintln(args)) }
func (g *grpcLogger) Infof(format string, args ...any)    { g.logger.Debug().Msgf(format, args...) }
func (g *grpcLogger) Warning(args ...any)                 { g.logger.Warn().Msg(fmt.Sprint(args...)) }
func (g *grpcLogger) Warningln(args ...any)               { g.logger.Warn().Msg(sprintln(args)) }
func (g *grpcLogger) Warningf(format string, args ...any) { g.logger.Warn().Msgf(format, args...) }
func (g *grpcLogger) Error(args ...any)                   { g.logger.Error().Msg(fmt.Sprint(args...)) }
func (g *grpcLogger) Errorln(args ...any)                 { g.logger.Error().Msg(sprintln(args)) }
func (g *grpcLogger) Errorf(format string, args ...any)   { g.logger.Error().Msgf(format, args...) }

// grpc expects Fatal to exit the process, zerolog's Fatal does
func (g *grpcLogger) Fatal(args ...any)                 { g.logger.Fatal().Msg(fmt.Sprint(args...)) }
func (g *grpcLogger) Fatalln(args ...any)               { g.logger.Fatal().Msg(sprintln(args)) }
func (g *grpcLogger) Fatalf(format string, args ...any) { g.logger.Fatal().Msgf(format, args...) }

// V gates grpc's extra verbose lines, too chatty for anything but trace
func (g *grpcLogger) V(int) bool {
	return zerolog.GlobalLevel() <= zerolog.TraceLevel
}
