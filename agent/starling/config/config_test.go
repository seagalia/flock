package config_test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v2"

	. "github.com/seagalia/flock/agent/starling/config"
)

var _ = Describe("Config", func() {
	var (
		loIface string
	)

	BeforeEach(func() {
		loIface = loopbackInterface()
	})

	// minimalConfig gives a valid config which does not rely on network discovery
	minimalConfig := func(extra string) string {
		return fmt.Sprintf(`
controller:
  addr: controller.local:443
  instance_uuid: my-instance
tls:
  cert_file: /tmp/cert.pem
  key_file: /tmp/key.pem
server:
  listen_host: 127.0.0.1
advertisement:
  interfaces: [%s]
%s
`, loIface, extra)
	}

	Context("ReadConfig", func() {
		var configPath string

		BeforeEach(func() {
			configPath = filepath.Join(GinkgoT().TempDir(), "config.yml")
		})

		It("should load a minimal config and apply defaults", func() {
			Expect(os.WriteFile(configPath, []byte(minimalConfig("")), 0644)).To(Succeed())

			cnf, err := ReadConfig(configPath)
			Expect(err).ToNot(HaveOccurred())

			Expect(cnf.ControllerConfig.Addr).To(Equal("controller.local:443"))
			Expect(cnf.ControllerConfig.InstanceUuid).To(Equal("my-instance"))
			Expect(cnf.ServerConfig.ListenHost).To(Equal("127.0.0.1"))
			Expect(cnf.ServerConfig.ListenPort).To(Equal(uint16(18000)))
			Expect(cnf.AdvertisementConfig.Interfaces).To(Equal([]string{loIface}))
			Expect(cnf.EnvoyConfig).ToNot(BeNil())
			Expect(cnf.EnvoyConfig.EnvoyExec.BinPath).To(Equal("envoy"))
			Expect(cnf.EnvoyConfig.MaxConnections).To(Equal(uint32(DefaultEnvoyMaxConnections)))
			Expect(cnf.WafConfig.WafEmbedded).To(BeTrue())
			Expect(cnf.Plugin).ToNot(BeNil())
			Expect(time.Duration(*cnf.WhenGarbageAfterStart)).To(Equal(10 * time.Minute))
			Expect(cnf.MetricsConfig.ProxyMetricsConfig.Targets).To(BeEmpty())
		})

		It("should refuse node_name, the name is the CN of the certificate", func() {
			Expect(os.WriteFile(configPath, []byte(minimalConfig("node_name: my-node")), 0644)).To(Succeed())

			_, err := ReadConfigStrict(configPath)
			Expect(err).To(MatchError(ContainSubstring("node_name")))
		})

		It("should refuse controller.agent_uuid, the controller gives it", func() {
			content := strings.Replace(minimalConfig(""), "instance_uuid:", "agent_uuid: a-1\n  instance_uuid:", 1)
			Expect(os.WriteFile(configPath, []byte(content), 0644)).To(Succeed())

			_, err := ReadConfigStrict(configPath)
			Expect(err).To(MatchError(ContainSubstring("agent_uuid")))
		})

		It("should give proxy metrics defaults to a metrics block without proxy", func() {
			Expect(os.WriteFile(configPath, []byte(minimalConfig("metrics: {}")), 0644)).To(Succeed())

			cnf, err := ReadConfig(configPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(cnf.MetricsConfig.ProxyMetricsConfig).ToNot(BeNil())
			Expect(cnf.MetricsConfig.ProxyMetricsConfig.Targets).To(BeEmpty())
		})

		It("should refuse unknown keys in strict mode only", func() {
			Expect(os.WriteFile(configPath, []byte(minimalConfig("envoy:\n  log_level: info")), 0644)).To(Succeed())

			_, err := ReadConfig(configPath)
			Expect(err).ToNot(HaveOccurred())
			_, err = ReadConfigStrict(configPath)
			Expect(err).To(MatchError(ContainSubstring("field log_level not found")))
		})

		It("should return an error when file does not exist", func() {
			_, err := ReadConfig(filepath.Join(GinkgoT().TempDir(), "not-exists.yml"))
			Expect(err).To(HaveOccurred())
		})
	})

	Context("Validation", func() {
		DescribeTable("should reject invalid config",
			func(target any, content string, errSubstring string) {
				err := yaml.Unmarshal([]byte(content), target)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(errSubstring))
			},
			Entry("without controller", &Config{}, `tls: {cert_file: a, key_file: b}`, "controller config is required"),
			Entry("with unknown advertisement interface", &AdvertisementConfig{},
				`{interfaces: [this-iface-does-not-exist]}`, "does not exist"),
			Entry("with envoy service without config file", &EnvoyService{},
				`{get_pid_command: [a], start_command: [b]}`, "config_file_path is required"),
			Entry("with envoy service without pid command", &EnvoyService{},
				`{config_file_path: a, start_command: [b]}`, "get_pid_command is required"),
			Entry("with envoy service without start command", &EnvoyService{},
				`{config_file_path: a, get_pid_command: [b]}`, "start_command is required"),
			Entry("with agent list without members", &AgentListConfig{}, `{listen_host: 127.0.0.1}`, "members is empty"),
			Entry("with metrics target without name", &ProxyMetricsTarget{}, `{url: http://localhost}`, "target name is empty"),
			Entry("with metrics target name with underscore", &ProxyMetricsTarget{}, `{name: my_name, url: http://localhost}`, "must not contains whitespace or underscore"),
			Entry("with metrics target without url nor socket path", &ProxyMetricsTarget{}, `{name: my-name}`, "target url or socket path is empty"),
			Entry("with metrics target with both url and socket path", &ProxyMetricsTarget{},
				`{name: my-name, url: http://localhost, socket_path: /tmp/metrics.sock}`, "cannot be set at the same time"),
			Entry("with plugin without name", &PluginHealthCheckConfig{}, `{description: a, path: ls}`, "missing name in plugin"),
			Entry("with plugin without description", &PluginHealthCheckConfig{}, `{name: a, path: ls}`, "missing description"),
			Entry("with plugin without path", &PluginHealthCheckConfig{}, `{name: a, description: a}`, "missing path"),
			Entry("with plugin with unknown path", &PluginHealthCheckConfig{}, `{name: a, description: a, path: /does/not/exist}`, "unable to find path"),
			Entry("with bgp without peers", &BGPConfig{}, `{local_asn: 65000}`, "at least one peer is required"),
			Entry("with bgp without local asn", &BGPConfig{}, `{peers: [{name: a, peer_asn: 65001, src_addr: 10.0.0.1}]}`, "local_asn is required"),
			Entry("with bgp peer without name", &BGPPeer{}, `{peer_asn: 65001, src_addr: 10.0.0.1}`, "name is required"),
			Entry("with bgp peer without peer asn", &BGPPeer{}, `{name: a, src_addr: 10.0.0.1}`, "asn is required"),
			Entry("with bgp peer with both src interface and addr", &BGPPeer{},
				`{name: a, peer_asn: 65001, src_addr: 10.0.0.1, src_interface: eth0}`, "mutually exclusive"),
			Entry("with bgp peer with unknown src interface", &BGPPeer{},
				`{name: a, peer_asn: 65001, src_interface: this-iface-does-not-exist}`, "does not exist"),
			Entry("with invalid bgp community", &BGPCommunity{}, `not-a-community`, "invalid community value"),
		)
	})

	Context("EnvoyConfig", func() {
		It("should default to exec envoy when neither exec nor service is set", func() {
			envoyConf := &EnvoyConfig{}
			Expect(yaml.Unmarshal([]byte(`log_in_json: true`), envoyConf)).To(Succeed())
			Expect(envoyConf.EnvoyExec).ToNot(BeNil())
			Expect(envoyConf.EnvoyExec.BinPath).To(Equal("envoy"))
			Expect(*envoyConf.LogInJson).To(BeTrue())
			Expect(envoyConf.MaxConnections).To(Equal(uint32(DefaultEnvoyMaxConnections)))
		})
		It("should keep the configured max connections", func() {
			envoyConf := &EnvoyConfig{}
			Expect(yaml.Unmarshal([]byte(`max_connections: 200000`), envoyConf)).To(Succeed())
			Expect(envoyConf.MaxConnections).To(Equal(uint32(200000)))
		})
		It("should default bin path when exec is set without it", func() {
			envoyConf := &EnvoyConfig{}
			Expect(yaml.Unmarshal([]byte(`exec: {log_level: debug}`), envoyConf)).To(Succeed())
			Expect(envoyConf.EnvoyExec.BinPath).To(Equal("envoy"))
			Expect(envoyConf.EnvoyExec.LogLevel).To(Equal("debug"))
		})
		It("should not set exec when service is set", func() {
			envoyConf := &EnvoyConfig{}
			Expect(yaml.Unmarshal([]byte(`
service:
  config_file_path: /etc/envoy.yml
  get_pid_command: [pidof, envoy]
  start_command: [systemctl, start, envoy]
`), envoyConf)).To(Succeed())
			Expect(envoyConf.EnvoyExec).To(BeNil())
			Expect(envoyConf.EnvoyService.StartCommand).To(Equal([]string{"systemctl", "start", "envoy"}))
		})
	})

	Context("WafConfig", func() {
		DescribeTable("should give the sockets the agent reaches starling-waf on",
			func(wafConf *WafConfig, socketPath, metricsSocketPath string) {
				Expect(wafConf.SocketPath()).To(Equal(socketPath))
				if metricsSocketPath != "" {
					Expect(wafConf.SocketMetricPath()).To(Equal(metricsSocketPath))
				}
			},
			Entry("embedded", &WafConfig{WafEmbedded: true}, DefaultSocketPath, ""),
			Entry("exec", &WafConfig{WafExec: &WafExec{BinPath: "starling-waf"}}, DefaultSocketPath, DefaultMetricsSocketPath),
			Entry("service", &WafConfig{WafService: &WafService{
				SocketPath:        "/run/starling-waf.sock",
				MetricsSocketPath: "/run/starling-waf-metrics.sock",
			}}, "/run/starling-waf.sock", "/run/starling-waf-metrics.sock"),
			Entry("external", &WafConfig{WafExternal: &WafExternal{
				SocketPath:        "/var/run/starling-waf/waf.sock",
				MetricsSocketPath: "/var/run/starling-waf/waf-metrics.sock",
			}}, "/var/run/starling-waf/waf.sock", "/var/run/starling-waf/waf-metrics.sock"),
		)
		It("should embed the WAF when neither exec nor service is set", func() {
			wafConf := &WafConfig{}
			Expect(yaml.Unmarshal([]byte(`{}`), wafConf)).To(Succeed())
			Expect(wafConf.WafEmbedded).To(BeTrue())
		})
		It("should default the bin path of an exec starling-waf", func() {
			wafConf := &WafConfig{}
			Expect(yaml.Unmarshal([]byte(`exec: {log_level: debug}`), wafConf)).To(Succeed())
			Expect(wafConf.WafEmbedded).To(BeFalse())
			Expect(wafConf.WafExec.BinPath).To(Equal("starling-waf"))
			Expect(wafConf.WafExec.LogLevel).To(Equal("debug"))
		})
		It("should read the sockets of a starling-waf service", func() {
			wafConf := &WafConfig{}
			Expect(yaml.Unmarshal([]byte(`
service:
  socket_path: /run/starling-waf.sock
  metrics_socket_path: /run/starling-waf-metrics.sock
  get_pid_command: [pidof, starling-waf]
  start_command: [systemctl, start, starling-waf]
`), wafConf)).To(Succeed())
			Expect(wafConf.WafEmbedded).To(BeFalse())
			Expect(wafConf.WafService.SocketPath).To(Equal("/run/starling-waf.sock"))
			Expect(wafConf.WafService.MetricsSocketPath).To(Equal("/run/starling-waf-metrics.sock"))
		})
		It("should require the metrics socket of a starling-waf service", func() {
			wafConf := &WafConfig{}
			err := yaml.Unmarshal([]byte(`
service:
  socket_path: /run/starling-waf.sock
  get_pid_command: [pidof, starling-waf]
  start_command: [systemctl, start, starling-waf]
`), wafConf)
			Expect(err).To(MatchError(ContainSubstring("metrics_socket_path is required")))
		})
		It("should read an external starling-waf with no command", func() {
			wafConf := &WafConfig{}
			Expect(yaml.Unmarshal([]byte(`
external:
  socket_path: /var/run/starling-waf/waf.sock
  metrics_socket_path: /var/run/starling-waf/waf-metrics.sock
`), wafConf)).To(Succeed())
			Expect(wafConf.WafEmbedded).To(BeFalse())
			Expect(wafConf.SocketPath()).To(Equal("/var/run/starling-waf/waf.sock"))
		})
		It("should require the socket of an external starling-waf", func() {
			wafConf := &WafConfig{}
			err := yaml.Unmarshal([]byte(`external: {metrics_socket_path: /var/run/starling-waf/waf-metrics.sock}`), wafConf)
			Expect(err).To(MatchError(ContainSubstring("socket_path is required")))
		})
	})

	Context("NetworkManagerConfig", func() {
		It("should default the marks", func() {
			nmConf := &NetworkManagerConfig{}
			Expect(yaml.Unmarshal([]byte(`{}`), nmConf)).To(Succeed())
			Expect(nmConf.SnatMark).To(Equal(uint(15181)))
			Expect(nmConf.SnatLogMark).To(Equal(uint(15182)))
			Expect(nmConf.LogOnlyMark).To(Equal(uint(15183)))
		})
		It("should read the mark keys", func() {
			nmConf := &NetworkManagerConfig{}
			Expect(yaml.Unmarshal([]byte(`{snat_mark: 1, snat_log_mark: 2, log_only_mark: 3}`), nmConf)).To(Succeed())
			Expect(nmConf.SnatMark).To(Equal(uint(1)))
			Expect(nmConf.SnatLogMark).To(Equal(uint(2)))
			Expect(nmConf.LogOnlyMark).To(Equal(uint(3)))
		})
		It("should refuse gen_comment in strict mode", func() {
			configPath := filepath.Join(GinkgoT().TempDir(), "config.yml")
			extra := "  network_manager:\n    gen_comment: x"
			Expect(os.WriteFile(configPath, []byte(minimalConfig(extra)), 0644)).To(Succeed())
			_, err := ReadConfigStrict(configPath)
			Expect(err).To(MatchError(ContainSubstring("field gen_comment not found")))
		})
	})

	Context("AgentListConfig", func() {
		It("should apply default port", func() {
			mlConf := &AgentListConfig{}
			Expect(yaml.Unmarshal([]byte(`{listen_host: 127.0.0.1, members: [10.0.0.1]}`), mlConf)).To(Succeed())
			Expect(mlConf.ListenPort).To(Equal(uint16(18001)))
			Expect(mlConf.ListenHost).To(Equal("127.0.0.1"))
			Expect(mlConf.Members).To(Equal([]string{"10.0.0.1"}))
		})
		It("should resolve listen host from listen interface", func() {
			mlConf := &AgentListConfig{}
			Expect(yaml.Unmarshal(fmt.Appendf(nil, `{listen_interface: %s, members: [10.0.0.1]}`, loIface), mlConf)).To(Succeed())
			Expect(mlConf.ListenHost).ToNot(BeEmpty())
		})
	})

	Context("Plugin", func() {
		It("should accept a plugin with a binary found in path", func() {
			plugin := &Plugin{}
			Expect(yaml.Unmarshal([]byte(`health_checks: [{name: a, description: a desc, path: ls, args: [-l]}]`), plugin)).To(Succeed())
			Expect(plugin.HealthChecks).To(HaveLen(1))
			Expect(plugin.HealthChecks[0].Args).To(Equal([]string{"-l"}))
		})
	})

	Context("MetricsConfig", func() {
		It("should parse proxy metrics targets", func() {
			metricsConf := &ProxyMetricsConfig{}
			Expect(yaml.Unmarshal([]byte(`
targets:
  - name: my-target
    url: http://localhost:9100/metrics
    user: user
    password: pass
    scrape_timeout: 5s
`), metricsConf)).To(Succeed())
			Expect(metricsConf.Targets).To(HaveLen(1))
			target := metricsConf.Targets[0]
			Expect(target.URL.URL.Host).To(Equal("localhost:9100"))
			Expect(target.URL.Raw).To(Equal("http://localhost:9100/metrics"))
			Expect(time.Duration(target.ScrapeTimeout)).To(Equal(5 * time.Second))
		})
		It("should parse a proxy metrics target on a unix socket", func() {
			metricsConf := &ProxyMetricsConfig{}
			Expect(yaml.Unmarshal([]byte(`
targets:
  - name: my-target
    socket_path: /tmp/metrics.sock
`), metricsConf)).To(Succeed())
			Expect(metricsConf.Targets[0].SocketPath).To(Equal("/tmp/metrics.sock"))
			Expect(metricsConf.Targets[0].URL.Raw).To(BeEmpty())
		})
	})

	Context("CIDR", func() {
		It("should parse a valid cidr", func() {
			cidr := &CIDR{}
			Expect(yaml.Unmarshal([]byte(`10.0.0.0/8`), cidr)).To(Succeed())
			Expect(cidr.IPNet.String()).To(Equal("10.0.0.0/8"))
		})
		It("should fail on invalid cidr", func() {
			cidr := &CIDR{}
			Expect(yaml.Unmarshal([]byte(`not-a-cidr`), cidr)).ToNot(Succeed())
		})
	})

	Context("BGP", func() {
		It("should parse a full bgp config with communities", func() {
			bgpConf := &BGPConfig{}
			Expect(yaml.Unmarshal([]byte(`
local_asn: 65000
router_id: 10.0.0.1
use_dynamic_placement: true
peers:
  - name: peer1
    peer_asn: 65001
    address: 10.0.0.254
    src_addr: 10.0.0.1
    hold_time: 90s
advertisements:
  - local_pref: 100
    communities: ["65000:100", "65000:200"]
    cidrs: [10.0.0.0/24]
`), bgpConf)).To(Succeed())
			Expect(bgpConf.LocalAsn).To(Equal(uint32(65000)))
			Expect(bgpConf.UseDynamicPlacement).To(BeTrue())

			Expect(bgpConf.Peers).To(HaveLen(1))
			peer := bgpConf.Peers[0]
			Expect(peer.Address).To(Equal("10.0.0.254"))
			Expect(peer.Port).To(Equal(uint16(179)), "default bgp port should be set")
			Expect(time.Duration(peer.HoldTime)).To(Equal(90 * time.Second))

			Expect(bgpConf.Advertisements).To(HaveLen(1))
			adv := bgpConf.Advertisements[0]
			Expect(adv.LocalPref).To(Equal(uint32(100)))
			Expect(adv.Communities).To(HaveLen(2))
			Expect(adv.Communities[0].Raw).To(Equal("65000:100"))
			Expect(adv.Communities[0].Asn).To(Equal(uint32(65000)))
			Expect(adv.Communities[0].Value).To(Equal(uint32(100)))
			Expect(adv.CIDRs[0].IPNet.String()).To(Equal("10.0.0.0/24"))
		})

		It("should accept a peer using a source interface", func() {
			peer := &BGPPeer{}
			Expect(yaml.Unmarshal(fmt.Appendf(nil, `{name: a, peer_asn: 65001, src_interface: %s, port: 1179}`, loIface), peer)).To(Succeed())
			Expect(peer.SrcInterface).To(Equal(loIface))
			Expect(peer.Port).To(Equal(uint16(1179)))
		})

		It("should give bfd defaults to a peer with bfd", func() {
			peer := &BGPPeer{}
			Expect(yaml.Unmarshal([]byte(`{name: a, peer_asn: 65001, src_addr: 10.0.0.1, bfd: {enabled: true}}`), peer)).To(Succeed())
			Expect(peer.Bfd.Enabled).To(BeTrue())
			Expect(peer.Bfd.Port).To(Equal(uint32(3784)))
			Expect(time.Duration(peer.Bfd.DesiredMinimumTxInterval)).To(Equal(300 * time.Millisecond))
			Expect(time.Duration(peer.Bfd.RequiredMinimumReceive)).To(Equal(300 * time.Millisecond))
			Expect(peer.Bfd.DetectionMultiplier).To(Equal(uint32(3)))

			withoutBfd := &BGPPeer{}
			Expect(yaml.Unmarshal([]byte(`{name: a, peer_asn: 65001, src_addr: 10.0.0.1}`), withoutBfd)).To(Succeed())
			Expect(withoutBfd.Bfd).To(BeNil())
		})

		It("should keep the bfd values of a peer", func() {
			peer := &BGPPeer{}
			Expect(yaml.Unmarshal([]byte(`{name: a, peer_asn: 65001, src_addr: 10.0.0.1, bfd: {enabled: true, port: 4784, desired_minimum_tx_interval: 1s, required_minimum_receive: 500ms, detection_multiplier: 5}}`), peer)).To(Succeed())
			Expect(peer.Bfd.Port).To(Equal(uint32(4784)))
			Expect(time.Duration(peer.Bfd.DesiredMinimumTxInterval)).To(Equal(time.Second))
			Expect(time.Duration(peer.Bfd.RequiredMinimumReceive)).To(Equal(500 * time.Millisecond))
			Expect(peer.Bfd.DetectionMultiplier).To(Equal(uint32(5)))
		})

		DescribeTable("ParseCommunity",
			func(raw string, expectedAsn, expectedValue uint32, expectErr bool) {
				asn, value, err := ParseCommunity(raw)
				if expectErr {
					Expect(err).To(HaveOccurred())
					return
				}
				Expect(err).ToNot(HaveOccurred())
				Expect(asn).To(Equal(expectedAsn))
				Expect(value).To(Equal(expectedValue))
			},
			Entry("valid community", "65000:100", uint32(65000), uint32(100), false),
			Entry("zero community", "0:0", uint32(0), uint32(0), false),
			Entry("without separator", "65000", uint32(0), uint32(0), true),
			Entry("with too many sections", "1:2:3", uint32(0), uint32(0), true),
			Entry("with asn overflowing 16 bits", "65536:1", uint32(0), uint32(0), true),
			Entry("with value overflowing 16 bits", "1:65536", uint32(0), uint32(0), true),
			Entry("with non numeric asn", "a:1", uint32(0), uint32(0), true),
			Entry("with non numeric value", "1:b", uint32(0), uint32(0), true),
		)
	})

	Context("Marshal", func() {
		It("writes custom types back in the format they are read from", func() {
			_, cidr, err := net.ParseCIDR("10.0.0.0/24")
			Expect(err).ToNot(HaveOccurred())
			community := &BGPCommunity{Raw: "65000:42", Asn: 65000, Value: 42}
			bgpConf := &BGPConfig{
				LocalAsn: 65000,
				Peers:    []*BGPPeer{{Name: "peer", PeerAsn: 65001, Address: "10.0.0.254", SrcAddr: "10.0.0.1"}},
				Advertisements: []*BGPAdvConfig{{
					LocalPref:   100,
					Communities: []*BGPCommunity{community},
					CIDRs:       []*CIDR{{IPNet: cidr}},
				}},
			}
			target := &ProxyMetricsTarget{Name: "target", URL: URLConfig{Raw: "http://localhost:9100/metrics"}}

			b, err := yaml.Marshal(bgpConf)
			Expect(err).ToNot(HaveOccurred())
			readBgp := &BGPConfig{}
			Expect(yaml.Unmarshal(b, readBgp)).To(Succeed(), string(b))
			Expect(readBgp.Advertisements[0].Communities[0].Raw).To(Equal("65000:42"))
			Expect(readBgp.Advertisements[0].CIDRs[0].IPNet.String()).To(Equal("10.0.0.0/24"))
			Expect(readBgp.Peers[0].Address).To(Equal("10.0.0.254"))

			b, err = yaml.Marshal(target)
			Expect(err).ToNot(HaveOccurred())
			readTarget := &ProxyMetricsTarget{}
			Expect(yaml.Unmarshal(b, readTarget)).To(Succeed(), string(b))
			Expect(readTarget.URL.Raw).To(Equal("http://localhost:9100/metrics"))
		})
	})
})
