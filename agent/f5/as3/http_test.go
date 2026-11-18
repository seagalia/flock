package as3_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
)

func httpVS(uuid string, port uint32, vhosts ...*route.VirtualHost) *vs.VirtualServer { //nolint:unparam // the call sites show the value
	return &vs.VirtualServer{UUID: uuid, Name: "vs-" + uuid[:4], Port: port, RouteConfig: &vs.VirtualServer_HttpRoutesConfig{
		HttpRoutesConfig: &vs.HttpRoutesConfig{VirtualHosts: vhosts},
	}}
}

func vhost(domains []string, routes ...*route.Route) *route.VirtualHost {
	return &route.VirtualHost{Domains: domains, Routes: routes}
}

func prefixRoute(prefix, backendUUID string) *route.Route {
	return &route.Route{
		Name:   "r" + prefix,
		Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: prefix}},
		Action: &route.Route_BackendUuid{BackendUuid: backendUUID},
	}
}

func httpInput(virtualServer *vs.VirtualServer, extra ...*cert.Certificate) as3.Input {
	in := input(newVIP(vipUUID, "10.99.0.1", vsUUID), virtualServer, staticBackend(backendUUID))
	for _, c := range extra {
		in.Certificates[c.GetUUID()] = c
	}
	return in
}

var _ = Describe("HTTP virtual servers with TLS and plain backends", func() {
	It("turns the server TLS on or off for each request, a keep-alive connection may reach both", func() {
		tlsBackend := staticBackend(otherBackendUUID)
		tlsBackend.EnableTls = true
		secure := prefixRoute("/secure", otherBackendUUID)
		in := httpInput(httpVS(vsUUID, 80, vhost([]string{"*"}, secure, prefixRoute("/", backendUUID))))
		in.Backends[otherBackendUUID] = tlsBackend

		rule := iRuleText(as3.Render(in), "http")

		appPath := "/flock/" + as3.AppName(vipUUID) + "/"
		Expect(rule).To(ContainSubstring("SSL::enable serverside\n        pool " + appPath + as3.PoolName(otherBackendUUID)))
		Expect(rule).To(ContainSubstring("SSL::disable serverside\n        pool " + appPath + as3.PoolName(backendUUID)))
	})
})

func iRuleText(res as3.Result, purpose string) string {
	rule, ok := res.Tenant.Apps[as3.AppName(vipUUID)].Objects[as3.IRuleName(purpose, vsUUID)].(*as3.IRule)
	ExpectWithOffset(1, ok).To(BeTrue(), "iRule %s not found", purpose)
	return rule.IRule
}

var _ = Describe("HTTP virtual servers", func() {
	It("routes with a generated iRule on a Service_HTTP", func() {
		res := as3.Render(httpInput(httpVS(vsUUID, 80, vhost([]string{"a.example.com"}, prefixRoute("/", backendUUID)))))

		svc := service(res, vipUUID, vsUUID)
		Expect(svc.Class).To(Equal("Service_HTTP"))
		Expect(svc.Pool).To(BeEmpty())
		Expect(svc.IRules).To(Equal([]any{as3.IRuleName("http", vsUUID)}))
		rule := iRuleText(res, "http")
		Expect(rule).To(ContainSubstring("when HTTP_REQUEST {"))
		Expect(rule).To(ContainSubstring(`set flock_host [string tolower [getfield [HTTP::host] ":" 1]]`))
		Expect(rule).To(ContainSubstring(`$flock_host eq "a.example.com"`))
		Expect(rule).To(ContainSubstring("pool /flock/" + as3.AppName(vipUUID) + "/" + as3.PoolName(backendUUID)))
		Expect(rule).To(ContainSubstring(`HTTP::respond 404 content "no route"`))
		Expect(pool(res, vipUUID, backendUUID)).ToNot(BeNil())
	})

	It("orders virtual hosts exact, suffix wildcards, prefix wildcards then catch all", func() {
		res := as3.Render(httpInput(httpVS(vsUUID, 80,
			vhost([]string{"*"}, prefixRoute("/", backendUUID)),
			vhost([]string{"api.*"}, prefixRoute("/", backendUUID)),
			vhost([]string{"*.example.com"}, prefixRoute("/", backendUUID)),
			vhost([]string{"*.b.example.com"}, prefixRoute("/", backendUUID)),
			vhost([]string{"a.example.com"}, prefixRoute("/", backendUUID)),
		)))

		rule := iRuleText(res, "http")
		exact := indexOf(rule, `$flock_host eq "a.example.com"`)
		longSuffix := indexOf(rule, `[string match "*.b.example.com" $flock_host]`)
		suffix := indexOf(rule, `[string match "*.example.com" $flock_host]`)
		prefix := indexOf(rule, `[string match "api.*" $flock_host]`)
		Expect(exact).To(BeNumerically("<", longSuffix))
		Expect(longSuffix).To(BeNumerically("<", suffix))
		Expect(suffix).To(BeNumerically("<", prefix))
		Expect(rule).To(ContainSubstring("} else {"))
	})

	DescribeTable("matches paths like the starling agent, with the path prefix of the virtual host",
		func(pathPrefix string, match *route.RouteMatch, condition string) {
			vh := vhost([]string{"*"}, &route.Route{Name: "r", Match: match, Action: &route.Route_BackendUuid{BackendUuid: backendUUID}})
			vh.PathPrefix = pathPrefix

			rule := iRuleText(as3.Render(httpInput(httpVS(vsUUID, 80, vh))), "http")

			Expect(rule).To(ContainSubstring("if { " + condition + " } {"))
		},
		Entry("a plain prefix", "", &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/api"}},
			`$flock_path starts_with "/api"`),
		Entry("a prefix after the path prefix", "/v1", &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/api"}},
			`$flock_path starts_with "/v1/api"`),
		Entry("the root route of a path prefix, on whole segments", "/api", &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
			`$flock_path eq "/api" || $flock_path starts_with "/api/"`),
		Entry("a path joined to a path prefix ending with a slash", "/api/", &route.RouteMatch{PathSpecifier: &route.RouteMatch_Path{Path: "/users"}},
			`$flock_path eq "/api/users"`),
	)

	It("matches every path on the root prefix", func() {
		rule := iRuleText(as3.Render(httpInput(httpVS(vsUUID, 80, vhost([]string{"*"}, prefixRoute("/", backendUUID))))), "http")

		Expect(rule).To(ContainSubstring("if { 1 } {"))
	})

	It("matches exact paths and regexes, case insensitive when asked", func() {
		exact := &route.Route{Name: "exact", Action: &route.Route_BackendUuid{BackendUuid: backendUUID},
			Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Path{Path: "/Login"}, CaseSensitive: wrapperspb.Bool(false)}}
		regex := &route.Route{Name: "regex", Action: &route.Route_BackendUuid{BackendUuid: backendUUID},
			Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_SafeRegex{SafeRegex: `/items/[0-9]+`}}}

		rule := iRuleText(as3.Render(httpInput(httpVS(vsUUID, 80, vhost([]string{"*"}, exact, regex)))), "http")

		Expect(rule).To(ContainSubstring(`$flock_lpath eq "/login"`))
		Expect(rule).To(ContainSubstring(`[regexp -- "^(?:/items/\[0-9\]+)\$" $flock_path]`))
	})

	It("quotes the strings it puts in the iRule", func() {
		rule := iRuleText(as3.Render(httpInput(httpVS(vsUUID, 80,
			vhost([]string{"a.example.com"}, prefixRoute(`/a"[exec]$x\`, backendUUID))))), "http")

		Expect(rule).To(ContainSubstring(`"/a\"\[exec\]\$x\\"`))
	})

	It("redirects", func() {
		redirect := &route.Route{Name: "redir", Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
			Action: &route.Route_Redirect{Redirect: &route.RedirectAction{
				SchemeRewriteSpecifier: &route.RedirectAction_HttpsRedirect{HttpsRedirect: true},
				HostRedirect:           "b.example.com",
				PathRedirect:           "/new",
			}}}

		rule := iRuleText(as3.Render(httpInput(httpVS(vsUUID, 80, vhost([]string{"*"}, redirect)))), "http")

		Expect(rule).To(ContainSubstring(`HTTP::respond 301 Location "https://b.example.com/new[expr {[HTTP::query] ne "" ? "?[HTTP::query]" : ""}]"`))
	})

	It("keeps the host and uri of the request in a redirect changing only the port", func() {
		redirect := &route.Route{Name: "redir", Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
			Action: &route.Route_Redirect{Redirect: &route.RedirectAction{PortRedirect: 8443}}}

		rule := iRuleText(as3.Render(httpInput(httpVS(vsUUID, 80, vhost([]string{"*"}, redirect)))), "http")

		Expect(rule).To(ContainSubstring(`HTTP::respond 301 Location "http://[getfield [HTTP::host] ":" 1]:8443[HTTP::uri]"`))
	})

	It("answers direct responses", func() {
		direct := &route.Route{Name: "direct", Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Path{Path: "/ping"}},
			Action: &route.Route_DirectResponse{DirectResponse: &route.DirectResponseAction{
				Status: 200, Specifier: &route.DirectResponseAction_InlineString{InlineString: "pong"},
			}}}

		rule := iRuleText(as3.Render(httpInput(httpVS(vsUUID, 80, vhost([]string{"*"}, direct)))), "http")

		Expect(rule).To(ContainSubstring(`HTTP::respond 200 content "pong"`))
	})

	It("answers 503 on a route whose backend is not known yet", func() {
		res := as3.Render(httpInput(httpVS(vsUUID, 80, vhost([]string{"*"}, prefixRoute("/", "unknown-backend")))))

		Expect(iRuleText(res, "http")).To(ContainSubstring(`HTTP::respond 503 content "no backend"`))
		Expect(res.Missing[vsKey(vsUUID)]).To(ConsistOf(backendKey("unknown-backend")))
	})

	It("terminates TLS with the certificates of the virtual hosts on a Service_HTTPS", func() {
		vh := vhost([]string{"a.example.com"}, prefixRoute("/", backendUUID))
		vh.TlsOptions = []*core.TLS{
			{ServerNames: []string{"a.example.com"}, CertificatesUuid: []string{certUUID}},
			{CertificatesUuid: []string{"55555555-5555-5555-5555-555555555555"}},
		}
		res := as3.Render(httpInput(httpVS(vsUUID, 443, vh), testCert(certUUID), testCert("55555555-5555-5555-5555-555555555555")))

		svc := service(res, vipUUID, vsUUID)
		Expect(svc.Class).To(Equal("Service_HTTPS"))
		Expect(*svc.Redirect80).To(BeFalse())
		Expect(svc.ServerTLS).To(Equal(as3.ServerTLSName(vsUUID)))
		objects := res.Tenant.Apps[as3.AppName(vipUUID)].Objects
		Expect(objects[as3.ServerTLSName(vsUUID)]).To(Equal(&as3.TLSServer{Class: "TLS_Server", Certificates: []as3.TLSServerCert{
			{Certificate: as3.CertName("55555555-5555-5555-5555-555555555555")},
			{Certificate: as3.CertName(certUUID), MatchToSNI: "a.example.com"},
		}}))
		Expect(objects[as3.CertName(certUUID)]).To(Equal(&as3.Certificate{
			Class: "Certificate", Remark: "cert-4444", Certificate: "CHAIN", PrivateKey: "KEY",
		}))
	})

	It("warns about a virtual host without TLS on a TLS virtual server", func() {
		withTLS := vhost([]string{"a.example.com"}, prefixRoute("/", backendUUID))
		withTLS.TlsOptions = []*core.TLS{{CertificatesUuid: []string{certUUID}}}
		res := as3.Render(httpInput(httpVS(vsUUID, 443, withTLS, vhost([]string{"b.example.com"}, prefixRoute("/", backendUUID))), testCert(certUUID)))

		Expect(res.Warnings[vsKey(vsUUID)]).To(ContainElement(
			"virtual host b.example.com has no TLS options, it is served over TLS with the certificates of the other virtual hosts"))
	})

	It("leaves out a certificate without key and the virtual server when none is usable", func() {
		vh := vhost([]string{"a.example.com"}, prefixRoute("/", backendUUID))
		vh.TlsOptions = []*core.TLS{{CertificatesUuid: []string{certUUID}}}
		broken := testCert(certUUID)
		broken.PrivateKey = ""

		res := as3.Render(httpInput(httpVS(vsUUID, 443, vh), broken))

		Expect(res.Warnings[vsKey(vsUUID)]).To(ContainElement("certificate cert-4444 has no chain or no key, it is left out"))
		Expect(res.Tenant.Apps[as3.AppName(vipUUID)].Objects).ToNot(HaveKey(as3.ServiceName(vsUUID)))
		Expect(res.Errors[vsKey(vsUUID)]).To(Equal("no usable certificate to terminate TLS"))
	})

	It("waits for a certificate not known yet", func() {
		vh := vhost([]string{"a.example.com"}, prefixRoute("/", backendUUID))
		vh.TlsOptions = []*core.TLS{{CertificatesUuid: []string{certUUID}}}

		res := as3.Render(httpInput(httpVS(vsUUID, 443, vh)))

		Expect(res.Missing[vsKey(vsUUID)]).To(ContainElement(as3.KeyOf(&cert.Certificate{UUID: certUUID})))
		Expect(res.Errors).To(BeEmpty())
	})

	It("reads the proxy protocol header with an iRule placed first", func() {
		virtualServer := httpVS(vsUUID, 80, vhost([]string{"*"}, prefixRoute("/", backendUUID)))
		virtualServer.GetHttpRoutesConfig().AcceptProxyProtocol = true

		res := as3.Render(httpInput(virtualServer))

		Expect(service(res, vipUUID, vsUUID).IRules).To(Equal([]any{as3.IRuleName("proxyrecv", vsUUID), as3.IRuleName("http", vsUUID)}))
		Expect(iRuleText(res, "proxyrecv")).To(ContainSubstring("when CLIENT_ACCEPTED"))
		Expect(res.Warnings[vsKey(vsUUID)]).To(ContainElement(
			"the proxy protocol header is read and removed, F5 keeps the address of the sending proxy as client address"))
	})
})

func indexOf(s, sub string) int {
	i := strings.Index(s, sub)
	ExpectWithOffset(1, i).To(BeNumerically(">=", 0), "not found: "+sub)
	return i
}
