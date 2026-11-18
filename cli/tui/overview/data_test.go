package overview

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
)

var _ = Describe("VsInfo", func() {
	It("gives one address per vip, ipv6 bracketed", func() {
		info := &VsInfo{
			vips: []*vip.VIP{{Ip: "10.0.0.1"}, {Ip: "2001:db8::1"}},
			vs:   &vs.VirtualServer{Port: 443},
		}

		Expect(info.Addresses()).To(Equal([]string{"10.0.0.1:443", "[2001:db8::1]:443"}))
	})

	DescribeTable("GetType",
		func(virtServ *vs.VirtualServer, want string) {
			Expect((&VsInfo{vs: virtServ}).GetType()).To(Equal(want))
		},
		Entry("tcp", &vs.VirtualServer{RouteConfig: &vs.VirtualServer_TcpRouteConfig{TcpRouteConfig: &vs.TcpRouteConfig{}}}, "TCP"),
		Entry("http", &vs.VirtualServer{RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{}}}, "HTTP"),
		Entry("tls", &vs.VirtualServer{RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{}}}, "TLS+TCP"),
		Entry("udp", &vs.VirtualServer{RouteConfig: &vs.VirtualServer_UdpRouteConfig{UdpRouteConfig: &vs.UdpRouteConfig{}}}, "UDP"),
		Entry("none", &vs.VirtualServer{}, "Unknown"),
	)

	It("is safe to use when nil", func() {
		var info *VsInfo

		Expect(info.GetUUID()).To(BeEmpty())
		Expect(info.GetName()).To(BeEmpty())
		Expect(info.GetBackends()).To(BeEmpty())
	})

	It("gives the backends of its routes", func() {
		info := &VsInfo{vs: &vs.VirtualServer{
			UUID:        "vs1",
			RouteConfig: &vs.VirtualServer_TcpRouteConfig{TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: "c1"}},
		}}

		Expect(info.GetUUID()).To(Equal("vs1"))
		Expect(info.GetBackends()).To(Equal([]string{"c1"}))
	})
})

var _ = Describe("BackendInfo", func() {
	DescribeTable("Type",
		func(backendInfo *backend.Backend, want string) {
			Expect((&BackendInfo{backend: backendInfo}).Type()).To(Equal(want))
		},
		Entry("dns", &backend.Backend{Endpoint: &backend.Backend_Dns{Dns: &backend.DNS{}}}, "DNS"),
		Entry("static", &backend.Backend{Endpoint: &backend.Backend_IpAddrGroup{IpAddrGroup: &core.IPAddrGroup{}}}, "Static"),
		Entry("eds", &backend.Backend{Endpoint: &backend.Backend_Eds{Eds: &backend.EDS{}}}, "EDS"),
		Entry("none", &backend.Backend{}, "Unknown"),
	)

	DescribeTable("Policy",
		func(policy backend.Backend_LbPolicy, want string) {
			Expect((&BackendInfo{backend: &backend.Backend{LbPolicy: policy}}).Policy()).To(Equal(want))
		},
		Entry("round robin", backend.Backend_ROUND_ROBIN, "Round Robin"),
		Entry("weighted round robin", backend.Backend_WEIGHTED_ROUND_ROBIN, "Weighted Round Robin"),
		Entry("weighted least request", backend.Backend_WEIGHTED_LEAST_REQUEST, "Weighted Least Request"),
		Entry("least request", backend.Backend_LEAST_REQUEST, "Least Request"),
		Entry("ring hash", backend.Backend_RING_HASH, "Ring Hash"),
		Entry("maglev", backend.Backend_MAGLEV, "Maglev"),
		Entry("unknown", backend.Backend_LbPolicy(99), "Unknown"),
	)

	DescribeTable("HealthcheckType",
		func(healthCheck *core.HealthCheck, want string) {
			Expect((&BackendInfo{backend: &backend.Backend{HealthCheck: healthCheck}}).HealthcheckType()).To(Equal(want))
		},
		Entry("http", &core.HealthCheck{HealthChecker: &core.HealthCheck_HttpHealthCheck{HttpHealthCheck: &core.HttpHealthCheck{}}}, "HTTP"),
		Entry("tcp", &core.HealthCheck{HealthChecker: &core.HealthCheck_TcpHealthCheck{TcpHealthCheck: &core.TcpHealthCheck{}}}, "TCP"),
		Entry("udp", &core.HealthCheck{HealthChecker: &core.HealthCheck_UdpHealthCheck{UdpHealthCheck: &core.UdpHealthCheck{}}}, "UDP"),
		Entry("grpc", &core.HealthCheck{HealthChecker: &core.HealthCheck_GrpcHealthCheck{GrpcHealthCheck: &core.GrpcHealthCheck{}}}, "GRPC"),
		Entry("none", &core.HealthCheck{HealthChecker: &core.HealthCheck_NoHealthCheck{NoHealthCheck: &core.NoHealthCheck{}}}, "None"),
		Entry("plugin", &core.HealthCheck{HealthChecker: &core.HealthCheck_PluginHealthCheck{PluginHealthCheck: &core.PluginHealthCheck{Name: "custom"}}}, "Plugin custom"),
		Entry("missing", nil, "Unknown"),
	)

	DescribeTable("EndpointConfig",
		func(backendInfo *backend.Backend, want string) {
			Expect((&BackendInfo{backend: backendInfo}).EndpointConfig()).To(Equal(want))
		},
		Entry("static", &backend.Backend{Endpoint: &backend.Backend_IpAddrGroup{IpAddrGroup: &core.IPAddrGroup{}}}, "static"),
		Entry("dns", &backend.Backend{Endpoint: &backend.Backend_Dns{Dns: &backend.DNS{Host: "backend.local", Port: 80}}},
			"dns backend.local:80 V6_PREFERRED"),
		Entry("eds", &backend.Backend{Endpoint: &backend.Backend_Eds{Eds: &backend.EDS{
			Url: &core.URL{Scheme: "https", Host: "eds.local", Port: 443}, ServiceName: "svc",
		}}}, "eds https://eds.local:443 service svc"),
		Entry("none", &backend.Backend{}, "unknown endpoints"),
	)

	It("shows the proxy protocol version only when enabled", func() {
		Expect((&BackendInfo{backend: &backend.Backend{}}).ProxyProtocol()).To(Equal("off"))
		Expect((&BackendInfo{backend: &backend.Backend{
			EnableProxyProtocol:  true,
			ProxyProtocolVersion: backend.Backend_V2,
		}}).ProxyProtocol()).To(Equal("V2"))
	})

	It("is safe to use when nil", func() {
		var info *BackendInfo

		Expect(info.GetUUID()).To(BeEmpty())
		Expect(info.GetName()).To(BeEmpty())
	})
})
