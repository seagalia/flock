package as3_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/sdk/flock/api/config/limit/v1"
	f5_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/f5_options/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
)

func renderTCPWith(opts *f5_vsopts.VirtualServerOptions, change func(*as3.Input)) as3.Result {
	virtualServer := tcpVS(vsUUID, 443, backendUUID)
	if opts != nil {
		virtualServer.GetTcpRouteConfig().TypedOptions = mustAny(opts)
	}
	in := input(newVIP(vipUUID, "10.99.0.1", vsUUID), virtualServer, staticBackend(backendUUID))
	if change != nil {
		change(&in)
	}
	return as3.Render(in)
}

var _ = Describe("Virtual server options", func() {
	It("attaches existing BIG-IP objects", func() {
		svc := service(renderTCPWith(&f5_vsopts.VirtualServerOptions{
			Irules:                     []string{"/Common/extra"},
			PersistenceProfile:         "/Common/persist",
			FallbackPersistenceProfile: "/Common/fallback",
			ClientTlsProfile:           "/Common/clientssl",
			ServerTlsProfile:           "/Common/serverssl",
			TcpProfile:                 "/Common/tcp",
			Snat:                       &f5_vsopts.VirtualServerOptions_Pool{Pool: "/Common/snatpool"},
		}, nil), vipUUID, vsUUID)

		Expect(svc.IRules).To(Equal([]any{as3.BigipRef{Bigip: "/Common/extra"}}))
		Expect(svc.PersistenceMethods).To(Equal([]any{as3.BigipRef{Bigip: "/Common/persist"}}))
		Expect(svc.FallbackPersistenceMethod).To(Equal(as3.BigipRef{Bigip: "/Common/fallback"}))
		Expect(svc.ServerTLS).To(Equal(as3.BigipRef{Bigip: "/Common/clientssl"}))
		Expect(svc.ClientTLS).To(Equal(as3.BigipRef{Bigip: "/Common/serverssl"}))
		Expect(svc.ProfileTCP).To(Equal(as3.BigipRef{Bigip: "/Common/tcp"}))
		Expect(svc.Snat).To(Equal(as3.BigipRef{Bigip: "/Common/snatpool"}))
	})

	It("attaches the http profile and the WAF policy to an http virtual server", func() {
		virtualServer := httpVS(vsUUID, 80, vhost([]string{"*"}, prefixRoute("/", backendUUID)))
		virtualServer.GetHttpRoutesConfig().TypedOptions = mustAny(&f5_vsopts.VirtualServerOptions{
			WafPolicy: "/Common/waf", HttpProfile: "/Common/http",
		})

		svc := service(as3.Render(httpInput(virtualServer)), vipUUID, vsUUID)

		Expect(svc.PolicyWAF).To(Equal(as3.BigipRef{Bigip: "/Common/waf"}))
		Expect(svc.ProfileHTTP).To(Equal(as3.BigipRef{Bigip: "/Common/http"}))
	})

	It("ignores the http options of a tcp virtual server", func() {
		res := renderTCPWith(&f5_vsopts.VirtualServerOptions{WafPolicy: "/Common/waf", HttpProfile: "/Common/http"}, nil)

		Expect(service(res, vipUUID, vsUUID).ProfileHTTP).To(BeNil())
		Expect(service(res, vipUUID, vsUUID).PolicyWAF).To(BeNil())
		Expect(res.Warnings[vsKey(vsUUID)]).To(ContainElements(
			"http_profile only applies to http virtual servers, it is ignored",
			"waf_policy only applies to http virtual servers, it is ignored"))
	})

	It("keeps the client address without snat", func() {
		svc := service(renderTCPWith(&f5_vsopts.VirtualServerOptions{Snat: &f5_vsopts.VirtualServerOptions_None{None: true}}, nil), vipUUID, vsUUID)

		Expect(svc.Snat).To(Equal("none"))
	})

	It("adds the extra iRules after the generated ones", func() {
		res := renderTCPWith(&f5_vsopts.VirtualServerOptions{Irules: []string{"/Common/extra"}}, func(in *as3.Input) {
			in.VirtualServers[vsUUID].GetTcpRouteConfig().AcceptProxyProtocol = true
		})

		Expect(service(res, vipUUID, vsUUID).IRules).To(Equal([]any{
			as3.IRuleName("proxyrecv", vsUUID), as3.BigipRef{Bigip: "/Common/extra"},
		}))
	})

	It("uses the udp profile on an udp virtual server", func() {
		virtualServer := udpVS(vsUUID, 53, backendUUID)
		virtualServer.GetUdpRouteConfig().TypedOptions = mustAny(&f5_vsopts.VirtualServerOptions{UdpProfile: "/Common/udp"})

		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), virtualServer, staticBackend(backendUUID)))

		Expect(service(res, vipUUID, vsUUID).ProfileUDP).To(Equal(as3.BigipRef{Bigip: "/Common/udp"}))
	})

	It("ignores starling options without a warning", func() {
		virtualServer := tcpVS(vsUUID, 443, backendUUID)
		virtualServer.GetTcpRouteConfig().TypedOptions = mustAny(&starling_vsopts.TcpUdpOptions{})

		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), virtualServer, staticBackend(backendUUID)))

		Expect(res.Warnings[vsKey(vsUUID)]).To(BeEmpty())
	})

	It("warns that l3 logs are not supported", func() {
		res := renderTCPWith(nil, func(in *as3.Input) { in.VirtualServers[vsUUID].EnableL3Log = true })

		Expect(res.Warnings[vsKey(vsUUID)]).To(ContainElement("enable_l3_log is not supported by the F5 agent, it is ignored"))
	})
})

var _ = Describe("Limits", func() {
	limited := func(limits ...*limit.Config) as3.Result {
		return renderTCPWith(nil, func(in *as3.Input) { in.VirtualServers[vsUUID].LimitConfig = limits })
	}

	It("limits the connections of the virtual server", func() {
		svc := service(limited(&limit.Config{Action: &limit.Config_ConnectionLimit{
			ConnectionLimit: &limit.ConnectionLimit{MaxConnections: 100},
		}}), vipUUID, vsUUID)

		Expect(svc.MaxConnections).To(BeEquivalentTo(100))
	})

	It("limits the connection rate per second", func() {
		res := limited(&limit.Config{Action: &limit.Config_ConnectionRateLimit{
			ConnectionRateLimit: &limit.ConnectionRateLimit{Rate: 90, Burst: 10, Unit: flocktype.TimeUnit_MINUTE},
		}})

		Expect(service(res, vipUUID, vsUUID).RateLimit).To(BeEquivalentTo(2))
		Expect(res.Warnings[vsKey(vsUUID)]).To(ContainElement("connection rate limit burst has no F5 equivalent, it is ignored"))
	})

	It("ignores the limits per address", func() {
		res := limited(&limit.Config{Action: &limit.Config_ConnectionLimit{
			ConnectionLimit: &limit.ConnectionLimit{MaxConnections: 100, SourceAddress: true},
		}})

		Expect(service(res, vipUUID, vsUUID).MaxConnections).To(BeZero())
		Expect(res.Warnings[vsKey(vsUUID)]).To(ContainElement(
			"limits per source or destination address are not supported by the F5 agent, the limit is ignored"))
	})

	It("ignores the typed limits without a warning", func() {
		res := limited(&limit.Config{Action: &limit.Config_TypedLimit{TypedLimit: mustAny(&starling_vsopts.TcpUdpOptions{})}})

		Expect(res.Warnings[vsKey(vsUUID)]).To(BeEmpty())
	})
})
