package as3_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
)

const authBackendUUID = "88888888-8888-8888-8888-888888888888"

func authPolicy(name string, upstreamHeaders ...string) *route.ExternalAuth {
	policy := &route.ExternalAuth{
		Name: name, BackendUuid: authBackendUUID, Path: "/check",
		RequestHeaders: []string{"Cookie"}, UpstreamHeaders: upstreamHeaders, ClientHeaders: []string{"set-cookie"},
	}
	policy.ProvDefault()
	return policy
}

// authVS has the policies sso (default of its virtual host) and spare (used by no route), a route opted out on
// /open/ and the root route checked by sso.
func authVS() *vs.VirtualServer {
	open := prefixRoute("/open/", backendUUID)
	open.ExternalAuth = &route.Route_ExternalAuthDisabled{ExternalAuthDisabled: true}
	app := vhost([]string{"*"}, open, prefixRoute("/", backendUUID))
	app.ExternalAuth = "sso"
	virtualServer := httpVS(vsUUID, 80, app)
	virtualServer.GetHttpRoutesConfig().ExternalAuths = []*route.ExternalAuth{
		authPolicy("sso", "x-auth-request-user"), authPolicy("spare", "x-spare"),
	}
	return virtualServer
}

func authInput(virtualServer *vs.VirtualServer) as3.Input {
	in := httpInput(virtualServer)
	in.Backends[authBackendUUID] = staticBackend(authBackendUUID)
	return in
}

func authService(res as3.Result, policyName string) *as3.Service {
	svc, _ := res.Tenant.Apps[as3.AppName(vipUUID)].Objects[as3.ExtAuthServiceName(vsUUID, policyName)].(*as3.Service)
	return svc
}

func extAuthRule(res as3.Result, policyName string) string { //nolint:unparam // the call sites show the value
	rule, ok := res.Tenant.Apps[as3.AppName(vipUUID)].Objects[as3.ExtAuthIRuleName(vsUUID, policyName)].(*as3.IRule)
	ExpectWithOffset(1, ok).To(BeTrue(), "iRule of policy %s not found", policyName)
	return rule.IRule
}

// branchOf gives the block of the if/elseif branch whose condition is cond.
func branchOf(rule, cond string) string {
	opening := "{ " + cond + " } {\n"
	start := strings.Index(rule, opening)
	ExpectWithOffset(1, start).To(BeNumerically(">=", 0), "no branch %q in\n%s", cond, rule)
	lineStart := strings.LastIndex(rule[:start], "\n") + 1
	lineIndent := rule[lineStart : lineStart+len(rule[lineStart:])-len(strings.TrimLeft(rule[lineStart:], " "))]
	body := rule[start+len(opening):]
	return body[:strings.Index(body, "\n"+lineIndent+"}")]
}

// braceDepths gives the lowest depth the unescaped braces of a TCL text reach and the depth at its end.
func braceDepths(text string) (lowest, end int) {
	escaped := false
	for _, c := range text {
		switch {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case c == '{':
			end++
		case c == '}':
			end--
			lowest = min(lowest, end)
		}
	}
	return lowest, end
}

var _ = Describe("External auth", func() {
	appPath := "/flock/" + as3.AppName(vipUUID) + "/"

	It("declares one internal auth service and one iRule per used policy", func() {
		res := as3.Render(authInput(authVS()))

		svc := authService(res, "sso")
		Expect(svc).ToNot(BeNil())
		Expect(svc.Class).To(Equal("Service_TCP"))
		Expect(svc.VirtualType).To(Equal("internal"))
		Expect(svc.VirtualAddresses).To(BeEmpty())
		Expect(svc.VirtualPort).To(BeZero())
		Expect(svc.Pool).To(Equal(as3.PoolName(authBackendUUID)))
		Expect(svc.Snat).To(Equal("auto"))
		Expect(svc.ClientTLS).To(BeNil())
		Expect(pool(res, vipUUID, authBackendUUID)).ToNot(BeNil())
		Expect(extAuthRule(res, "sso")).To(ContainSubstring("proc check {} {"))

		Expect(authService(res, "spare")).To(BeNil())
		Expect(res.Tenant.Apps[as3.AppName(vipUUID)].Objects).ToNot(HaveKey(as3.ExtAuthIRuleName(vsUUID, "spare")))
		Expect(res.Missing).To(BeEmpty())
	})

	It("connects to the auth backend with TLS when it has enable_tls", func() {
		in := authInput(authVS())
		in.Backends[authBackendUUID].EnableTls = true

		res := as3.Render(in)

		Expect(authService(res, "sso").ClientTLS).To(Equal(as3.ClientTLSName(authBackendUUID)))
		Expect(service(res, vipUUID, vsUUID).ClientTLS).To(BeNil())
	})

	It("strips the upstream headers of every policy before routing", func() {
		rule := iRuleText(as3.Render(authInput(authVS())), "http")

		Expect(rule).To(ContainSubstring("set flock_lpath [string tolower $flock_path]\n" +
			"    HTTP::header remove \"x-auth-request-user\"\n" +
			"    HTTP::header remove \"x-spare\"\n"))
	})

	It("checks only the routes using a policy", func() {
		rule := iRuleText(as3.Render(authInput(authVS())), "http")

		Expect(branchOf(rule, `$flock_path starts_with "/open/"`)).ToNot(ContainSubstring("::check"))
		Expect(branchOf(rule, "1")).To(MatchRegexp(`^\s+if \{ !\[call ` + appPath + as3.ExtAuthIRuleName(vsUUID, "sso") +
			`::check\] \} \{ return \}\n\s+pool ` + appPath + as3.PoolName(backendUUID)))
	})

	It("checks a route with the policy it names over the default of its virtual host", func() {
		virtualServer := authVS()
		root := virtualServer.GetHttpRoutesConfig().GetVirtualHosts()[0].GetRoutes()[1]
		root.ExternalAuth = &route.Route_ExternalAuthName{ExternalAuthName: "spare"}

		res := as3.Render(authInput(virtualServer))

		Expect(branchOf(iRuleText(res, "http"), "1")).To(ContainSubstring(as3.ExtAuthIRuleName(vsUUID, "spare") + "::check"))
		Expect(authService(res, "sso")).To(BeNil())
		Expect(authService(res, "spare")).ToNot(BeNil())
	})

	It("puts the policy values in the proc", func() {
		virtualServer := authVS()
		virtualServer.GetHttpRoutesConfig().GetExternalAuths()[0].Timeout = durationpb.New(1500 * time.Millisecond)

		rule := extAuthRule(as3.Render(authInput(virtualServer)), "sso")

		Expect(rule).To(ContainSubstring(`"/check"`))
		Expect(rule).To(ContainSubstring("+ 1500"))
		Expect(rule).To(ContainSubstring(`"` + appPath + as3.ExtAuthServiceName(vsUUID, "sso") + `"`))
		Expect(rule).To(ContainSubstring(`[list "authorization" "cookie"]`))
		Expect(rule).To(ContainSubstring(`[list "x-auth-request-user"]`))
		Expect(rule).To(ContainSubstring(`[list "set-cookie" "location" "www-authenticate"]`))
		Expect(rule).To(ContainSubstring(`HTTP::respond 503 content ""`))
		Expect(rule).To(ContainSubstring("transfer-encoding"))
	})

	It("lets the request go on when the auth service fails and failure_mode is ALLOW", func() {
		virtualServer := authVS()
		virtualServer.GetHttpRoutesConfig().GetExternalAuths()[0].FailureMode = route.FailureMode_ALLOW

		rule := extAuthRule(as3.Render(authInput(virtualServer)), "sso")

		Expect(rule).ToNot(ContainSubstring("HTTP::respond 503"))
		Expect(rule).To(ContainSubstring("        log local0.warn \"flock external auth " + virtualServer.GetName() +
			"/sso failed: $flock_err\"\n        return 1\n    }\n"))
	})

	It("keeps the braces of a virtual server name and a check path balanced in the iRules", func() {
		virtualServer := authVS()
		virtualServer.Name = "vs-}{ close"
		virtualServer.GetHttpRoutesConfig().GetExternalAuths()[0].Path = "/check}x{y}"
		virtualServer.GetHttpRoutesConfig().GetVirtualHosts()[0].GetRoutes()[0].Match =
			&route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/open}/"}}

		res := as3.Render(authInput(virtualServer))

		for _, rule := range []string{extAuthRule(res, "sso"), iRuleText(res, "http")} {
			lowest, end := braceDepths(rule)
			Expect(lowest).To(BeZero(), rule)
			Expect(end).To(BeZero(), rule)
		}
		Expect(extAuthRule(res, "sso")).To(ContainSubstring(`"/check\}x\{y\}"`))
	})

	It("sends the client address alone as x-forwarded-for", func() {
		rule := extAuthRule(as3.Render(authInput(authVS())), "sso")

		Expect(rule).To(ContainSubstring(`X-Forwarded-For: [IP::client_addr]\r\n"`))
		Expect(rule).ToNot(ContainSubstring(`"x-forwarded-for"`))
	})

	It("reads no answer body after a HEAD request nor with a bodiless status", func() {
		rule := extAuthRule(as3.Render(authInput(authVS())), "sso")

		Expect(rule).To(ContainSubstring("        if { [HTTP::method] eq \"HEAD\" || $flock_status == 204 || $flock_status == 304 } {\n" +
			"            set flock_length 0\n        }\n        set flock_body \"\"\n"))
	})

	It("reads the answer as TCL 8.4 can't misread it, within the deadline", func() {
		rule := extAuthRule(as3.Render(authInput(authVS())), "sso")

		Expect(rule).To(ContainSubstring(`([1-9][0-9][0-9])`))
		Expect(rule).To(ContainSubstring(`[scan $flock_value %d flock_length] != 1`))
		Expect(rule).ToNot(ContainSubstring("-idle"))
		Expect(rule).To(ContainSubstring("connect -timeout $flock_left "))
		Expect(rule).To(ContainSubstring("        if { $flock_left <= 0 } {\n            error \"connected too late to send\"\n" +
			"        }\n        send -timeout $flock_left "))
		Expect(rule).To(ContainSubstring(`[string map [list "\r" ""] [string range $flock_line`))
	})

	It("passes no client header HTTP::respond would read as one of its options", func() {
		virtualServer := authVS()
		virtualServer.GetHttpRoutesConfig().GetExternalAuths()[0].ClientHeaders = []string{"set-cookie", "content", "noserver", "version", "-reset"}

		res := as3.Render(authInput(virtualServer))

		Expect(extAuthRule(res, "sso")).To(ContainSubstring(`[list "set-cookie" "location" "www-authenticate"]`))
		Expect(res.Warnings[vsKey(vsUUID)]).To(ContainElement(
			"external auth sso: client header content is read as an option of HTTP::respond, it is never passed to the client"))
		Expect(res.Warnings[vsKey(vsUUID)]).To(ContainElement(
			"external auth sso: client header -reset is read as an option of HTTP::respond, it is never passed to the client"))
	})

	It("checks a route answering a redirect or a direct response before its action", func() {
		virtualServer := authVS()
		app := virtualServer.GetHttpRoutesConfig().GetVirtualHosts()[0]
		moved := prefixRoute("/moved/", backendUUID)
		moved.Action = &route.Route_Redirect{Redirect: &route.RedirectAction{
			SchemeRewriteSpecifier: &route.RedirectAction_HttpsRedirect{HttpsRedirect: true},
		}}
		answered := prefixRoute("/answered/", backendUUID)
		answered.Action = &route.Route_DirectResponse{DirectResponse: &route.DirectResponseAction{
			Status: 200, Specifier: &route.DirectResponseAction_InlineString{InlineString: "ok"},
		}}
		app.Routes = append([]*route.Route{moved, answered}, app.GetRoutes()...)

		rule := iRuleText(as3.Render(authInput(virtualServer)), "http")

		check := `^\s+if \{ !\[call ` + appPath + as3.ExtAuthIRuleName(vsUUID, "sso") + `::check\] \} \{ return \}\n`
		Expect(branchOf(rule, `$flock_path starts_with "/moved/"`)).To(MatchRegexp(check + `\s+HTTP::respond 301 Location `))
		Expect(branchOf(rule, `$flock_path starts_with "/answered/"`)).To(MatchRegexp(check + `\s+HTTP::respond 200 content "ok"`))
	})

	It("leaves no auth object behind a virtual server waiting for its certificate", func() {
		virtualServer := authVS()
		virtualServer.GetHttpRoutesConfig().GetVirtualHosts()[0].TlsOptions = []*core.TLS{{CertificatesUuid: []string{certUUID}}}

		res := as3.Render(authInput(virtualServer))

		objects := res.Tenant.Apps[as3.AppName(vipUUID)].Objects
		Expect(objects).ToNot(HaveKey(as3.ServiceName(vsUUID)))
		Expect(objects).ToNot(HaveKey(as3.ExtAuthServiceName(vsUUID, "sso")))
		Expect(objects).ToNot(HaveKey(as3.ExtAuthIRuleName(vsUUID, "sso")))
		Expect(objects).ToNot(HaveKey(as3.PoolName(authBackendUUID)))
	})

	It("leaves no certificate behind a virtual server waiting for its policy backend", func() {
		virtualServer := authVS()
		virtualServer.GetHttpRoutesConfig().GetVirtualHosts()[0].TlsOptions = []*core.TLS{{CertificatesUuid: []string{certUUID}}}

		res := as3.Render(httpInput(virtualServer, testCert(certUUID)))

		objects := res.Tenant.Apps[as3.AppName(vipUUID)].Objects
		Expect(objects).ToNot(HaveKey(as3.ServiceName(vsUUID)))
		Expect(objects).ToNot(HaveKey(as3.CertName(certUUID)))
		Expect(objects).ToNot(HaveKey(as3.ServerTLSName(vsUUID)))
	})

	It("leaves the virtual server out while the policy backend is unknown", func() {
		res := as3.Render(httpInput(authVS()))

		Expect(res.Tenant.Apps[as3.AppName(vipUUID)].Objects).ToNot(HaveKey(as3.ServiceName(vsUUID)))
		Expect(authService(res, "sso")).To(BeNil())
		Expect(res.Missing[vsKey(vsUUID)]).To(ConsistOf(backendKey(authBackendUUID)))
	})

	It("renders no check nor auth service for a default policy of a virtual host with no routes", func() {
		empty := vhost([]string{"*"})
		empty.ExternalAuth = "sso"
		virtualServer := httpVS(vsUUID, 80, empty)
		virtualServer.GetHttpRoutesConfig().ExternalAuths = []*route.ExternalAuth{authPolicy("sso", "x-auth-request-user")}

		res := as3.Render(authInput(virtualServer))

		Expect(iRuleText(res, "http")).ToNot(ContainSubstring("::check"))
		Expect(authService(res, "sso")).To(BeNil())
	})

	It("strips nothing on a virtual server with no policy", func() {
		res := as3.Render(httpInput(httpVS(vsUUID, 80, vhost([]string{"*"}, prefixRoute("/", backendUUID)))))

		Expect(iRuleText(res, "http")).ToNot(ContainSubstring("HTTP::header remove"))
	})
})
