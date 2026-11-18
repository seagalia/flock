package agentconfig_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"go.yaml.in/yaml/v2"

	. "github.com/seagalia/flock/sdk/agentsdk/agentconfig"
)

var _ = Describe("Agent config blocks", func() {
	DescribeTable("should reject invalid config",
		func(target any, content string, errSubstring string) {
			err := yaml.Unmarshal([]byte(content), target)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(errSubstring))
		},
		Entry("without controller addr", &ControllerConfig{}, `{instance_uuid: a}`, "controller addr is required"),
		Entry("without controller instance uuid", &ControllerConfig{}, `{addr: a}`, "controller instance_uuid is required"),
		Entry("without tls cert", &TlsConfig{}, `{key_file: b}`, "tls cert_file is required"),
		Entry("without tls key", &TlsConfig{}, `{cert_file: b}`, "tls key_file is required"),
		Entry("with syslog without targets", &SyslogConfig{}, `{format_rfc3164: true}`, "syslog targets is empty"),
	)

	Context("ServerConfig", func() {
		It("should resolve listen host from listen interface", func() {
			serverConf := &ServerConfig{}
			Expect(yaml.Unmarshal(fmt.Appendf(nil, `{listen_interface: %s, listen_port: 9000}`, loopbackInterface()), serverConf)).To(Succeed())
			Expect(serverConf.ListenHost).ToNot(BeEmpty())
			Expect(serverConf.ListenPort).To(Equal(uint16(9000)))
		})
		It("should fail when listen interface does not exist", func() {
			serverConf := &ServerConfig{}
			err := yaml.Unmarshal([]byte(`{listen_interface: this-iface-does-not-exist}`), serverConf)
			Expect(err).To(HaveOccurred())
		})
		It("should default the port of a missing server block", func() {
			serverConf, err := DefaultServerConfig()
			Expect(err).ToNot(HaveOccurred())
			Expect(serverConf.ListenPort).To(Equal(uint16(18000)))
		})
	})

	Context("Log", func() {
		It("should read log settings without applying them", func() {
			levelBefore := zerolog.GlobalLevel()
			logConf := &Log{}
			Expect(yaml.Unmarshal([]byte(`{level: debug, in_json: true, filter_components: [envoy]}`), logConf)).To(Succeed())
			Expect(logConf.Level).To(Equal("debug"))
			Expect(logConf.InJson).To(BeTrue())
			Expect(logConf.FilterComponents).To(Equal([]string{"envoy"}))
			Expect(zerolog.GlobalLevel()).To(Equal(levelBefore))
		})
		It("should fail on invalid log level", func() {
			logConf := &Log{}
			Expect(yaml.Unmarshal([]byte(`{level: not-a-level}`), logConf)).ToNot(Succeed())
		})
		It("should default structured data on syslog", func() {
			logConf := &Log{}
			Expect(yaml.Unmarshal([]byte(`{syslog: {targets: ["udp://localhost:514"]}}`), logConf)).To(Succeed())
			Expect(logConf.Syslog.StructuredData).ToNot(BeNil())
		})
	})
})
