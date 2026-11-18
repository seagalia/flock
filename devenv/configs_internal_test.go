package devenv

import (
	"net"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	starlingconfig "github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("Configs", func() {
	var (
		certs *pki
		dir   string
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		var err error
		certs, err = ensurePki(filepath.Join(dir, "pki"), nil)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should load the controller config like controller serve does", func() {
		storePath := filepath.Join(dir, "store.db")
		cnf, err := writeControllerConfig(certs, controllerSettings{
			StorePath: storePath, LogLevel: "debug", ListenAddr: "0.0.0.0:8443", AdminPassword: "s3cret",
		}, filepath.Join(dir, "controller.yml"))
		Expect(err).ToNot(HaveOccurred())

		Expect(cnf.Log.Level).To(Equal("debug"))
		Expect(cnf.Server.ListenAddr).To(Equal("0.0.0.0:8443"))
		Expect(cnf.StoreManager.SqlStorer.DbType).To(Equal("sqlite"))
		Expect(cnf.StoreManager.SqlStorer.DSN).To(HavePrefix(storePath + "?"))
		Expect(cnf.Server.JwtMaker.Alg.Alg).ToNot(BeNil())
		Expect(cnf.Server.AdminConfig.Users).To(ConsistOf("admin"))
		Expect(cnf.Server.AdminConfig.Groups).To(ConsistOf("agent-local-provisioner"))
		Expect(cnf.Server.Dashboard.Enabled).To(BeTrue())
		Expect(cnf.Server.LoopbackHttpAddr).To(Equal("127.0.0.1:8480"))
		Expect(cnf.Server.Login.Type).To(Equal("basic"))
		Expect(cnf.Server.Login.BasicAuth[0].Username).To(Equal("admin"))
		Expect(cnf.Server.Login.BasicAuth[0].Password).To(Equal("s3cret"))
		Expect(cnf.Mtls.ServerName).To(Equal(agentServerName))
		Expect(cnf.IPAM.Disabled).To(BeTrue())
		Expect(cnf.Instantiators).To(HaveLen(1))
		Expect(cnf.Instantiators[0].Zones).To(ConsistOf(zoneName))
		agents := cnf.Instantiators[0].StaticInstantiator.Agents
		Expect(agents).To(HaveLen(2))
		Expect(agents[0].Name).To(Equal("local"))
		Expect(agents[0].Host).To(Equal(agentHost))
		Expect(agents[0].Mode.ToAgentMode()).To(Equal(agent.Agent_SIDECAR))
		Expect(agents[0].Features.ShowLogs).To(BeTrue())

		Expect(agents[1].Name).To(Equal("local-provisioner"))
		Expect(agents[1].UUID).To(Equal(provisionerUuid))
		Expect(agents[1].Host).To(Equal("127.0.0.1:18002"))
		Expect(agents[1].Type).To(Equal("provisioner"))
		Expect(agents[1].Mode.ToAgentMode()).To(Equal(agent.Agent_PROVISIONER))
		Expect(agents[1].Features.ShowLogs).To(BeFalse())
		Expect(agents[1].Features.ShowMetrics).To(BeTrue())
		Expect(agents[1].Features.ShowStatus).To(BeTrue())

		for _, declared := range agents {
			Expect(helpers.Validate(&svclookup.ReadAgentRequest{AgentUuid: declared.UUID})).To(Succeed())
		}
	})

	It("should load the agent config like agent starling start does", func() {
		cnf, err := writeAgentConfig(certs, agentSettings{
			InstanceUuid: "instance-uuid", EnvoyPath: "/path/to/envoy", LogLevel: "debug", ControllerAddr: "127.0.0.1:8443",
		}, filepath.Join(dir, "agent.yml"))
		Expect(err).ToNot(HaveOccurred())

		Expect(cnf.Log.Level).To(Equal("debug"))
		Expect(cnf.DevMode).To(BeTrue())
		Expect(cnf.ControllerConfig.Addr).To(Equal("127.0.0.1:8443"))
		Expect(cnf.ControllerConfig.InstanceUuid).To(Equal("instance-uuid"))
		Expect(cnf.ServerConfig.ListenHost).To(Equal("127.0.0.1"))
		Expect(cnf.EnvoyConfig.EnvoyExec.BinPath).To(Equal("/path/to/envoy"))
		Expect(cnf.EnvoyConfig.EnvoyService).To(BeNil())
		loopback, err := loopbackInterface()
		Expect(err).ToNot(HaveOccurred())
		Expect(cnf.AdvertisementConfig.Interfaces).To(ConsistOf(loopback))
	})

	It("should leave dev mode for a privileged agent and use the given controller address", func() {
		cnf, err := writeAgentConfig(certs, agentSettings{
			InstanceUuid: "instance-uuid", EnvoyPath: "/path/to/envoy", LogLevel: "debug",
			ControllerAddr: "10.0.0.5:8443", Privileged: true,
		}, filepath.Join(dir, "agent.yml"))
		Expect(err).ToNot(HaveOccurred())

		Expect(cnf.DevMode).To(BeFalse())
		Expect(cnf.ControllerConfig.Addr).To(Equal("10.0.0.5:8443"))
		Expect(cnf.System.SysctlMode).To(Equal(starlingconfig.SysctlModePermissive))
	})

	It("should add the interfaces to source-NAT on to the loopback", func() {
		// the config validation wants an interface of this machine
		ifaces, err := net.Interfaces()
		Expect(err).ToNot(HaveOccurred())
		snatIface := ""
		for _, iface := range ifaces {
			if iface.Flags&net.FlagLoopback == 0 {
				snatIface = iface.Name
				break
			}
		}
		if snatIface == "" {
			Skip("no interface besides the loopback")
		}

		cnf, err := writeAgentConfig(certs, agentSettings{
			InstanceUuid: "instance-uuid", EnvoyPath: "/path/to/envoy", ControllerAddr: "10.0.0.5:8443",
			Privileged: true, SnatIfaces: []string{snatIface},
		}, filepath.Join(dir, "agent.yml"))
		Expect(err).ToNot(HaveOccurred())

		loopback, err := loopbackInterface()
		Expect(err).ToNot(HaveOccurred())
		Expect(cnf.AdvertisementConfig.Interfaces).To(ConsistOf(loopback, snatIface))
	})
})

var _ = Describe("internalControllerAddr", func() {
	DescribeTable("gives the address the apps of the process dial",
		func(host, expected string) {
			Expect(internalControllerAddr(host)).To(Equal(expected))
		},
		Entry("loopback", "127.0.0.1", "127.0.0.1:8443"),
		Entry("unspecified v4", "0.0.0.0", "127.0.0.1:8443"),
		Entry("unspecified v6", "::", "127.0.0.1:8443"),
		Entry("other loopback v4", "127.0.0.2", "127.0.0.2:8443"),
		Entry("loopback v6", "::1", "[::1]:8443"),
		Entry("v4", "10.0.0.5", "10.0.0.5:8443"),
		Entry("v6", "fd00::5", "[fd00::5]:8443"),
	)
})

var _ = Describe("isExposed", func() {
	DescribeTable("tells if the controller listens beyond the loopback",
		func(host string, expected bool) {
			Expect(isExposed(host)).To(Equal(expected))
		},
		Entry("127.0.0.1", "127.0.0.1", false),
		Entry("::1", "::1", false),
		Entry("localhost", "localhost", false),
		Entry("0.0.0.0", "0.0.0.0", true),
		Entry("10.0.0.5", "10.0.0.5", true),
	)
})
