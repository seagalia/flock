package logging

import (
	"log"
	"strings"

	"github.com/rs/zerolog"
)

// HTTPServerErrorLog is the ErrorLog of an http.Server, without it the server writes its errors to stderr outside zerolog.
// A client closing the connection before the TLS handshake is only debug: TCP health checks and port scans do it
// all the time.
func HTTPServerErrorLog(component string) *log.Logger {
	return log.New(&httpErrorWriter{logger: Component(component)}, "", 0)
}

type httpErrorWriter struct {
	logger zerolog.Logger
}

func (w *httpErrorWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	isHandshakeError := strings.HasPrefix(line, "http: TLS handshake error")
	closedBeforeHandshake := isHandshakeError && strings.HasSuffix(line, ": EOF")
	switch {
	case closedBeforeHandshake:
		w.logger.Debug().Msg(line)
	case isHandshakeError:
		w.logger.Warn().Msg(line)
	default:
		w.logger.Error().Msg(line)
	}
	return len(p), nil
}
