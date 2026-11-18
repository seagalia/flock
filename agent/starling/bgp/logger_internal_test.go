package bgp

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
)

var _ = Describe("zerologSlogHandler", func() {
	var (
		buf    *bytes.Buffer
		logger *slog.Logger
	)

	BeforeEach(func() {
		buf = &bytes.Buffer{}
		previous := zerolog.GlobalLevel()
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
		DeferCleanup(zerolog.SetGlobalLevel, previous)
		base := zerolog.New(buf).With().Str("component", "gobgp_server").Logger()
		logger = slog.New(&zerologSlogHandler{logger: base})
	})

	It("writes records as log lines with their attributes", func() {
		logger.With("Topic", "Peer").Warn("peer down", "Key", "10.0.0.1", "State", 6)
		line := map[string]any{}
		Expect(json.Unmarshal(buf.Bytes(), &line)).To(Succeed())
		Expect(line).To(HaveKeyWithValue("message", "peer down"))
		Expect(line).To(HaveKeyWithValue("level", "warn"))
		Expect(line).To(HaveKeyWithValue("component", "gobgp_server"))
		Expect(line).To(HaveKeyWithValue("Topic", "Peer"))
		Expect(line).To(HaveKeyWithValue("Key", "10.0.0.1"))
		Expect(line).To(HaveKeyWithValue("State", float64(6)))
	})

	It("follows the global level", func() {
		logger.Debug("noisy")
		Expect(buf.Len()).To(BeZero())
		Expect(logger.Enabled(context.Background(), slog.LevelDebug)).To(BeFalse())
		Expect(logger.Enabled(context.Background(), slog.LevelError)).To(BeTrue())
	})
})
