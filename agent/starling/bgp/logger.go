package bgp

import (
	"context"
	"log/slog"

	"github.com/rs/zerolog"

	"github.com/seagalia/flock/sdk/logging"
)

// gobgp logs through slog, zerologSlogHandler sends its records to zerolog like the rest of the agent
type zerologSlogHandler struct {
	logger zerolog.Logger
}

func newBgpLogger() *slog.Logger {
	return slog.New(&zerologSlogHandler{logger: logging.Component("gobgp_server")})
}

func (h *zerologSlogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return toZerologLevel(level) >= zerolog.GlobalLevel()
}

func (h *zerologSlogHandler) Handle(_ context.Context, record slog.Record) error {
	event := h.logger.WithLevel(toZerologLevel(record.Level)) //nolint:zerologlint // sent by Msg once attrs are added
	record.Attrs(func(attr slog.Attr) bool {
		event = event.Interface(attr.Key, attr.Value.Any())
		return true
	})
	event.Msg(record.Message)
	return nil
}

func (h *zerologSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	logger := h.logger.With()
	for _, attr := range attrs {
		logger = logger.Interface(attr.Key, attr.Value.Any())
	}
	return &zerologSlogHandler{logger: logger.Logger()}
}

// gobgp does not use groups, attributes stay flat
func (h *zerologSlogHandler) WithGroup(string) slog.Handler {
	return h
}

func toZerologLevel(level slog.Level) zerolog.Level {
	switch {
	case level >= slog.LevelError:
		return zerolog.ErrorLevel
	case level >= slog.LevelWarn:
		return zerolog.WarnLevel
	case level >= slog.LevelInfo:
		return zerolog.InfoLevel
	default:
		return zerolog.DebugLevel
	}
}
