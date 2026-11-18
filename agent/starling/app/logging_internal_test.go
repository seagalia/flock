package app

import (
	"bytes"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/leodido/go-syslog/v4/rfc5424"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog/log"

	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
	"github.com/seagalia/flock/sdk/logging"
)

var _ = Describe("setupLogging", func() {
	It("setupLogging sends lines to syslog with the agent header", func() {
		server, err := net.ListenPacket("udp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(server.Close)
		DeferCleanup(logging.Setup, logging.Options{Console: GinkgoWriter})

		app := &App{name: "node-1", cnf: &config.Config{
			ControllerConfig: &agentconfig.ControllerConfig{InstanceUuid: "uuid-1"},
			Log: agentconfig.Log{
				Syslog: &agentconfig.SyslogConfig{Targets: []string{"udp://" + server.LocalAddr().String()}},
			},
		}}
		Expect(app.setupLogging()).To(Succeed())
		log.Info().Msg("hello")

		packet := make([]byte, 4096)
		Expect(server.SetReadDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		n, _, err := server.ReadFrom(packet)
		Expect(err).ToNot(HaveOccurred())

		parsed, err := rfc5424.NewParser().Parse(bytes.TrimSpace(packet[:n]))
		Expect(err).ToNot(HaveOccurred())
		message := parsed.(*rfc5424.SyslogMessage)
		Expect(*message.Appname).To(Equal("flock-agent"))
		Expect(*message.Hostname).To(Equal("node-1.uuid-1.agent"))
		Expect(*message.ProcID).To(Equal(strconv.Itoa(os.Getpid())))
		Expect(*message.Message).To(Equal("hello"))
		Expect(app.listenerHook.LatestLines()).To(ContainElement(ContainSubstring("hello")))
	})
})
