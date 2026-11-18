package as3_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
)

const otherBackendUUID = "77777777-7777-7777-7777-777777777777"

func tlsVS(uuid string, routes ...*vs.TlsTcpRouteConfig) *vs.VirtualServer { //nolint:unparam // the call sites show the value
	return &vs.VirtualServer{UUID: uuid, Name: "vs-" + uuid[:4], Port: 443, RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{
		TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: routes},
	}}
}

func terminate(backendUUID string, serverNames ...string) *vs.TlsTcpRouteConfig { //nolint:unparam // the call sites show the value
	return &vs.TlsTcpRouteConfig{BackendUuid: backendUUID, Mode: &vs.TlsTcpRouteConfig_Tls{
		Tls: &core.TLS{ServerNames: serverNames, CertificatesUuid: []string{certUUID}},
	}}
}

func passthrough(backendUUID string, serverNames ...string) *vs.TlsTcpRouteConfig {
	return &vs.TlsTcpRouteConfig{BackendUuid: backendUUID, Mode: &vs.TlsTcpRouteConfig_Passthrough{
		Passthrough: &vs.TlsPassthrough{ServerNames: serverNames},
	}}
}

func tlsInput(virtualServer *vs.VirtualServer) as3.Input {
	in := input(newVIP(vipUUID, "10.99.0.1", vsUUID), virtualServer, staticBackend(backendUUID),
		staticBackend(otherBackendUUID), testCert(certUUID))
	return in
}

var _ = Describe("TLS over TCP virtual servers", func() {
	It("terminates TLS for the termination routes and routes on the SNI", func() {
		res := as3.Render(tlsInput(tlsVS(vsUUID, terminate(backendUUID, "a.example.com"))))

		svc := service(res, vipUUID, vsUUID)
		Expect(svc.Class).To(Equal("Service_TCP"))
		Expect(svc.ServerTLS).To(Equal(as3.ServerTLSName(vsUUID)))
		Expect(svc.IRules).To(Equal([]any{as3.IRuleName("sni", vsUUID)}))
		rule := iRuleText(res, "sni")
		Expect(rule).To(ContainSubstring("when CLIENT_ACCEPTED {\n    SSL::disable\n    TCP::collect\n}"))
		Expect(rule).To(ContainSubstring(`if { $flock_sni eq "a.example.com" } {`))
		Expect(rule).To(ContainSubstring("SSL::enable\n"))
		Expect(rule).To(ContainSubstring("pool /flock/" + as3.AppName(vipUUID) + "/" + as3.PoolName(backendUUID)))
		Expect(rule).To(ContainSubstring("reject"))
	})

	It("passes TLS through without any SSL command", func() {
		res := as3.Render(tlsInput(tlsVS(vsUUID, passthrough(backendUUID, "*.example.com"))))

		Expect(service(res, vipUUID, vsUUID).ServerTLS).To(BeNil())
		rule := iRuleText(res, "sni")
		Expect(rule).ToNot(ContainSubstring("SSL::"))
		Expect(rule).To(ContainSubstring(`[string match "*.example.com" $flock_sni]`))
	})

	It("mixes termination and passthrough routes, exact names first then wildcards then the default route", func() {
		res := as3.Render(tlsInput(tlsVS(vsUUID,
			passthrough(otherBackendUUID),
			terminate(backendUUID, "*.example.com"),
			passthrough(otherBackendUUID, "db.example.com"),
		)))

		rule := iRuleText(res, "sni")
		exact := indexOf(rule, `$flock_sni eq "db.example.com"`)
		wildcard := indexOf(rule, `[string match "*.example.com" $flock_sni]`)
		fallback := indexOf(rule, "} else {")
		Expect(exact).To(BeNumerically("<", wildcard))
		Expect(wildcard).To(BeNumerically("<", fallback))
		Expect(rule).To(ContainSubstring("SSL::enable"))
		Expect(rule).ToNot(ContainSubstring("reject\n    }\n    TCP::release"))
		Expect(res.Errors).To(BeEmpty())
	})

	It("strips the proxy protocol header before reading the ClientHello", func() {
		virtualServer := tlsVS(vsUUID, passthrough(backendUUID))
		virtualServer.GetTlsTcpRoutesConfig().AcceptProxyProtocol = true

		res := as3.Render(tlsInput(virtualServer))

		rule := iRuleText(res, "sni")
		Expect(indexOf(rule, "flock_pp_done")).To(BeNumerically("<", indexOf(rule, "flock_tls_type")))
		Expect(service(res, vipUUID, vsUUID).IRules).To(HaveLen(1))
		Expect(res.Warnings[vsKey(vsUUID)]).To(ContainElement(
			"the proxy protocol header is read and removed, F5 keeps the address of the sending proxy as client address"))
	})

	It("leaves the TLS of a passthrough route to the client", func() {
		in := tlsInput(tlsVS(vsUUID, passthrough(backendUUID)))
		in.Backends[backendUUID].EnableTls = true

		res := as3.Render(in)

		Expect(service(res, vipUUID, vsUUID).ClientTLS).To(BeNil())
		Expect(iRuleText(res, "sni")).ToNot(ContainSubstring("SSL::"))
		Expect(res.Warnings[vsKey(vsUUID)]).To(ContainElement(
			"backend backend-3333 connects with TLS, a passthrough route forwards the client TLS as is"))
	})

	It("ignores the starling options of a route without a warning", func() {
		route := passthrough(backendUUID)
		route.TypedOptions = mustAny(&starling_vsopts.TlsTcpOptions{})

		res := as3.Render(tlsInput(tlsVS(vsUUID, route)))

		Expect(res.Warnings[vsKey(vsUUID)]).To(BeEmpty())
	})
})

var _ = Describe("Proxy protocol towards the endpoints", func() {
	It("sends a v1 header with a backend iRule", func() {
		b := staticBackend(backendUUID)
		b.EnableProxyProtocol = true

		res := renderBackend(b)

		name := as3.IRuleName("proxysend", backendUUID)
		Expect(service(res, vipUUID, vsUUID).IRules).To(Equal([]any{name}))
		rule := res.Tenant.Apps[as3.AppName(vipUUID)].Objects[name].(*as3.IRule)
		Expect(rule.IRule).To(ContainSubstring("when SERVER_CONNECTED"))
		// an http or tls virtual server reaches several backends, only the endpoints of this one get the header
		Expect(rule.IRule).To(ContainSubstring(`if { [LB::server pool] eq "/flock/` + as3.AppName(vipUUID) + "/" + as3.PoolName(backendUUID) + `" } {`))
	})

	It("sends v1 when v2 is asked", func() {
		b := staticBackend(backendUUID)
		b.EnableProxyProtocol = true
		b.ProxyProtocolVersion = backend.Backend_V2

		Expect(renderBackend(b).Warnings[backendKey(backendUUID)]).To(ContainElement(
			"proxy protocol v2 is not generated by the F5 agent, v1 is sent"))
	})

	It("sends nothing to the endpoints of an udp virtual server", func() {
		b := staticBackend(backendUUID)
		b.EnableProxyProtocol = true

		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), udpVS(vsUUID, 53, backendUUID), b))

		Expect(service(res, vipUUID, vsUUID).IRules).To(BeEmpty())
		Expect(res.Warnings[backendKey(backendUUID)]).To(ContainElement(
			"proxy protocol is not sent to the endpoints of an udp virtual server"))
	})
})
