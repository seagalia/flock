package serves

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	core "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/limit/v1"
	route "github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

var _ = Describe("checkVipPorts", func() {
	// httpsVs has one virtual host per server name, without server names it has one catch-all virtual host
	httpsVs := func(name string, port uint32, serverNames ...string) *vs.VirtualServer { //nolint:unparam // the call sites show the value
		hosts := []*route.VirtualHost{}
		for _, serverName := range serverNames {
			hosts = append(hosts, &route.VirtualHost{
				Domains:    []string{serverName},
				TlsOptions: []*core.TLS{{ServerNames: []string{serverName}, CertificatesUuid: []string{"cert-1"}}},
			})
		}
		if len(serverNames) == 0 {
			hosts = append(hosts, &route.VirtualHost{
				Domains:    []string{"*"},
				TlsOptions: []*core.TLS{{CertificatesUuid: []string{"cert-1"}}},
			})
		}
		return &vs.VirtualServer{Name: name, UUID: name + "-uuid", Port: port, RouteConfig: &vs.VirtualServer_HttpRoutesConfig{
			HttpRoutesConfig: &vs.HttpRoutesConfig{VirtualHosts: hosts},
		}}
	}
	tlsRoutesVs := func(name string, port uint32, routes ...*vs.TlsTcpRouteConfig) *vs.VirtualServer {
		return &vs.VirtualServer{Name: name, UUID: name + "-uuid", Port: port, RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{
			TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: routes},
		}}
	}
	tlsVs := func(name string, port uint32, serverNames ...string) *vs.VirtualServer {
		return tlsRoutesVs(name, port, &vs.TlsTcpRouteConfig{
			BackendUuid: "backend-1",
			Mode:        &vs.TlsTcpRouteConfig_Tls{Tls: &core.TLS{ServerNames: serverNames, CertificatesUuid: []string{"cert-1"}}},
		})
	}
	passthroughVs := func(name string, port uint32, serverNames ...string) *vs.VirtualServer {
		return tlsRoutesVs(name, port, &vs.TlsTcpRouteConfig{
			BackendUuid: "backend-1",
			Mode:        &vs.TlsTcpRouteConfig_Passthrough{Passthrough: &vs.TlsPassthrough{ServerNames: serverNames}},
		})
	}
	tcpVs := func(name string, port uint32) *vs.VirtualServer {
		return &vs.VirtualServer{Name: name, UUID: name + "-uuid", Port: port, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
			TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: "backend-1"},
		}}
	}
	udpVs := func(name string, port uint32) *vs.VirtualServer {
		return &vs.VirtualServer{Name: name, UUID: name + "-uuid", Port: port, RouteConfig: &vs.VirtualServer_UdpRouteConfig{
			UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: "backend-1"},
		}}
	}
	mustAny := func(m proto.Message) *anypb.Any {
		GinkgoHelper()
		a, err := anypb.New(m)
		Expect(err).ToNot(HaveOccurred())
		return a
	}

	It("accepts one virtual server per port, and tcp and udp on the same port number", func() {
		Expect(checkVipPorts([]*vs.VirtualServer{tcpVs("dns-tcp", 53), udpVs("dns-udp", 53), httpsVs("web", 443)})).To(Succeed())
	})

	It("accepts https and tls virtual servers with distinct server names on one port", func() {
		servers := []*vs.VirtualServer{
			httpsVs("web", 443, "web.local"),
			tlsVs("db", 443, "db.local"),
			passthroughVs("git", 443, "git.local"),
		}
		Expect(checkVipPorts(servers)).To(Succeed())
	})

	It("accepts one catch-all on a shared port", func() {
		Expect(checkVipPorts([]*vs.VirtualServer{httpsVs("web", 443, "web.local"), passthroughVs("any", 443)})).To(Succeed())
	})

	It("refuses two catch-alls on a shared port", func() {
		err := checkVipPorts([]*vs.VirtualServer{httpsVs("a", 443), passthroughVs("b", 443)})
		Expect(err).To(MatchError(And(ContainSubstring("port tcp/443 is used by virtual servers a, b"), ContainSubstring("catch-all"))))
	})

	It("refuses a server name in two virtual servers, whatever its case", func() {
		err := checkVipPorts([]*vs.VirtualServer{httpsVs("web", 443, "web.local"), tlsVs("db", 443, "WEB.local")})
		Expect(err).To(MatchError(And(ContainSubstring("port tcp/443 is used by virtual servers web, db"), ContainSubstring("web.local"))))
	})

	DescribeTable("refuses a port shared with tcp or udp virtual servers",
		func(servers []*vs.VirtualServer, port string) {
			Expect(checkVipPorts(servers)).To(MatchError(ContainSubstring("port " + port + " is used by virtual servers")))
		},
		Entry("tcp next to tls", []*vs.VirtualServer{tcpVs("raw", 443), tlsVs("db", 443, "db.local")}, "tcp/443"),
		Entry("two tcp", []*vs.VirtualServer{tcpVs("a", 80), tcpVs("b", 80)}, "tcp/80"),
		Entry("two udp", []*vs.VirtualServer{udpVs("dns", 53), udpVs("dns2", 53)}, "udp/53"),
	)

	It("refuses an http virtual server with a virtual host without TLS on a shared port", func() {
		plain := httpsVs("web", 443, "web.local")
		plain.GetHttpRoutesConfig().VirtualHosts = append(plain.GetHttpRoutesConfig().VirtualHosts, &route.VirtualHost{Domains: []string{"plain.local"}})

		err := checkVipPorts([]*vs.VirtualServer{plain, tlsVs("db", 443, "db.local")})
		Expect(err).To(MatchError(ContainSubstring("TLS")))
	})

	It("accepts l3 log on one virtual server of a shared port", func() {
		logged := tlsVs("db", 443, "db.local")
		logged.EnableL3Log = true

		Expect(checkVipPorts([]*vs.VirtualServer{httpsVs("web", 443, "web.local"), logged})).To(Succeed())
	})

	DescribeTable("refuses virtual servers disagreeing on a setting of the port",
		func(change func(*vs.VirtualServer)) {
			changed := tlsVs("db", 443, "db.local")
			change(changed)

			err := checkVipPorts([]*vs.VirtualServer{httpsVs("web", 443, "web.local"), changed})
			Expect(err).To(MatchError(ContainSubstring("port tcp/443 is used by virtual servers web, db: web and db disagree on the settings of the port")))
		},
		Entry("proxy protocol", func(v *vs.VirtualServer) {
			v.GetTlsTcpRoutesConfig().AcceptProxyProtocol = true
		}),
		Entry("connection limit", func(v *vs.VirtualServer) {
			v.LimitConfig = []*limit.Config{{Action: &limit.Config_ConnectionLimit{ConnectionLimit: &limit.ConnectionLimit{MaxConnections: 10}}}}
		}),
		Entry("connection rate limit", func(v *vs.VirtualServer) {
			v.LimitConfig = []*limit.Config{{Action: &limit.Config_ConnectionRateLimit{ConnectionRateLimit: &limit.ConnectionRateLimit{Rate: 10, Burst: 5}}}}
		}),
	)

	It("leaves the starling options of a shared port to the agent", func() {
		changed := tlsVs("db", 443, "db.local")
		changed.GetTlsTcpRoutesConfig().TypedOptions = mustAny(&starling_vsopts.TlsTcpOptions{DisableSnat: true})

		Expect(checkVipPorts([]*vs.VirtualServer{httpsVs("web", 443, "web.local"), changed})).To(Succeed())
	})

	It("accepts http options of their own on a shared port", func() {
		web := httpsVs("web", 443, "web.local")
		web.GetHttpRoutesConfig().TypedOptions = mustAny(&starling_vsopts.HttpOptions{RequestHeadersToRemove: []string{"x-debug"}})

		Expect(checkVipPorts([]*vs.VirtualServer{web, tlsVs("db", 443, "db.local")})).To(Succeed())
	})

	It("accepts virtual servers with their own http rate limit on a shared port", func() {
		limited := httpsVs("web", 443, "web.local")
		limited.LimitConfig = []*limit.Config{{Action: &limit.Config_TypedLimit{TypedLimit: mustAny(&starling_limit.StarlingRateLimit{})}}}

		Expect(checkVipPorts([]*vs.VirtualServer{limited, tlsVs("db", 443, "db.local")})).To(Succeed())
	})
})
