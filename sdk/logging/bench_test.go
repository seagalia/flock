package logging_test

import (
	"io"
	"testing"

	"github.com/seagalia/flock/sdk/logging"
)

func BenchmarkDisabledDebug(b *testing.B) {
	if err := logging.Setup(logging.Options{Console: io.Discard}); err != nil {
		b.Fatal(err)
	}
	logger := logging.Component("bench")
	b.ReportAllocs()
	for b.Loop() {
		logger.Debug().Str("vip", "10.0.0.1").Msg("adding vip")
	}
}

func BenchmarkEnabledInfo(b *testing.B) {
	if err := logging.Setup(logging.Options{Console: io.Discard, InJson: true}); err != nil {
		b.Fatal(err)
	}
	logger := logging.Component("bench")
	b.ReportAllocs()
	for b.Loop() {
		logger.Info().Str("vip", "10.0.0.1").Msg("adding vip")
	}
}
