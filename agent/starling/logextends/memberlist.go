package logextends

import (
	"strings"

	"github.com/rs/zerolog"
)

// MemberlistWriter is the output of memberlist's std logger, which writes one line per Write with its level as a
// prefix
type MemberlistWriter struct {
	Logger zerolog.Logger
}

func (w *MemberlistWriter) Write(p []byte) (int, error) {
	line := strings.TrimSuffix(string(p), "\n")
	level := zerolog.InfoLevel
	if after, ok := strings.CutPrefix(line, "[ERR] "); ok {
		line, level = after, zerolog.ErrorLevel
	}
	if after, ok := strings.CutPrefix(line, "[WARN] "); ok {
		line, level = after, zerolog.WarnLevel
	}
	if after, ok := strings.CutPrefix(line, "[DEBUG] "); ok {
		line, level = after, zerolog.DebugLevel
	}
	w.Logger.WithLevel(level).Msg(line)
	return len(p), nil
}
