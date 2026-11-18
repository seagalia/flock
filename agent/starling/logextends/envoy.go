// Package logextends turns the output of envoy and memberlist into flock log lines with their real level.
package logextends

import (
	"bytes"
	"encoding/json"

	"github.com/rs/zerolog"
)

// EnvoyLines emits the lines envoy prints. Envoy app logs (stderr, json) give their level, message and source
// location; json lines on stdout (access logs) become fields.
type EnvoyLines struct {
	// carries component=envoy and the source of the lines
	Logger zerolog.Logger
	// "stdout" or "stderr", added as field output
	Output string
	// lines are envoy access logs: msg_id=access_log and message "access log"
	AccessLog bool
	partial   []byte
}

type jsonAppLog struct {
	ThreadID string `json:"thread_id"`
	LogLevel string `json:"log_level"`
	Logger   string `json:"logger_name"`
	File     string `json:"file"`
	Message  string `json:"message"`
}

// Write splits what envoy prints into lines, a line cut between two writes is completed by the next one
func (e *EnvoyLines) Write(p []byte) (int, error) {
	e.partial = append(e.partial, p...)
	for {
		end := bytes.IndexByte(e.partial, '\n')
		if end < 0 {
			return len(p), nil
		}
		e.Emit(string(e.partial[:end]))
		e.partial = e.partial[end+1:]
	}
}

// Flush emits what is left of a line without new line, the last words of an envoy that stopped mid line
func (e *EnvoyLines) Flush() {
	e.Emit(string(e.partial))
	e.partial = nil
}

func (e *EnvoyLines) Emit(line string) {
	if line == "" {
		return
	}
	isJson := line[0] == '{'
	switch {
	case !isJson:
		e.event(zerolog.InfoLevel).Msg(line)
	case e.Output == "stderr":
		e.emitAppLog(line)
	default:
		e.emitJsonOutput(line)
	}
}

func (e *EnvoyLines) event(level zerolog.Level) *zerolog.Event {
	// WithLevel and not Fatal(): a critical envoy line must not exit the agent
	event := e.Logger.WithLevel(level).Str("output", e.Output) //nolint:zerologlint // callers send it with Msg
	if e.AccessLog {
		event = event.Str("msg_id", "access_log")
	}
	return event
}

func (e *EnvoyLines) emitAppLog(line string) {
	var appLog jsonAppLog
	if err := json.Unmarshal([]byte(line), &appLog); err != nil {
		// a line we cannot read is kept as it is rather than lost
		e.event(zerolog.InfoLevel).Msg(line)
		return
	}
	e.event(toLevel(appLog.LogLevel)).
		Str("thread_id", appLog.ThreadID).
		Str("logger", appLog.Logger).
		Str("file", appLog.File).
		Msg(appLog.Message)
}

func (e *EnvoyLines) emitJsonOutput(line string) {
	fields := map[string]any{}
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		e.event(zerolog.InfoLevel).Msg(line)
		return
	}
	message := "access log"
	if !e.AccessLog {
		message, _ = fields["message"].(string)
		delete(fields, "message")
	}
	e.event(zerolog.InfoLevel).Fields(fields).Msg(message)
}

func toLevel(envoyLevel string) zerolog.Level {
	switch envoyLevel {
	case "critical":
		return zerolog.FatalLevel
	case "error":
		return zerolog.ErrorLevel
	case "warn":
		return zerolog.WarnLevel
	case "debug":
		return zerolog.DebugLevel
	case "trace":
		return zerolog.TraceLevel
	default:
		return zerolog.InfoLevel
	}
}
