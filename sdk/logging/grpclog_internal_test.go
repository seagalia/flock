package logging

import (
	"bytes"
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/grpclog"
)

var _ = Describe("grpc logger", func() {
	var (
		buf    *bytes.Buffer
		logger grpclog.LoggerV2
	)

	lastLine := func() map[string]any {
		lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
		fields := map[string]any{}
		Expect(json.Unmarshal([]byte(lines[len(lines)-1]), &fields)).To(Succeed())
		return fields
	}

	BeforeEach(func() {
		buf = &bytes.Buffer{}
		logger = newGrpcLogger(zerolog.New(buf))
		previous := zerolog.GlobalLevel()
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
		DeferCleanup(zerolog.SetGlobalLevel, previous)
	})

	It("maps grpc info to debug", func() {
		logger.Infof("picked %d", 1)
		Expect(lastLine()).To(And(HaveKeyWithValue("level", "debug"), HaveKeyWithValue("message", "picked 1")))
		logger.Info("plain ", "info")
		Expect(lastLine()).To(And(HaveKeyWithValue("level", "debug"), HaveKeyWithValue("message", "plain info")))
		logger.Infoln("line", "info")
		Expect(lastLine()).To(And(HaveKeyWithValue("level", "debug"), HaveKeyWithValue("message", "line info")))
	})

	It("keeps warning and error levels", func() {
		logger.Warning("w")
		Expect(lastLine()).To(HaveKeyWithValue("level", "warn"))
		logger.Errorln("e")
		Expect(lastLine()).To(And(HaveKeyWithValue("level", "error"), HaveKeyWithValue("message", "e")))
	})

	It("enables verbosity only at trace", func() {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		Expect(logger.V(2)).To(BeFalse())
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
		Expect(logger.V(2)).To(BeTrue())
	})

	It("routes grpclog to the configured console with component grpc", func() {
		Expect(Setup(Options{Level: "debug", InJson: true, Console: buf})).To(Succeed())
		DeferCleanup(Setup, Options{Console: GinkgoWriter})
		grpclog.Info("hello")
		Expect(lastLine()).To(And(
			HaveKeyWithValue("component", "grpc"),
			HaveKeyWithValue("level", "debug"),
			HaveKeyWithValue("message", "hello"),
		))
	})
})
