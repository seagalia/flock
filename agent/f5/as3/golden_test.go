package as3_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/limit/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	f5_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/f5_options/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
)

// go run github.com/onsi/ginkgo/v2/ginkgo ./agent/f5/as3 -- -update rewrites the golden files
var updateGolden = flag.Bool("update", false, "rewrite the golden declarations of testdata")

// goldenScenarios are the inputs of the golden declarations. The lab e2e posts each golden file to a real BIG-IP
// with dry-run, so they also prove AS3 accepts what the agent renders.
var goldenScenarios = map[string]func() as3.Input{
	"tcp": func() as3.Input {
		return input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), staticBackend(backendUUID))
	},
	"dns": func() as3.Input {
		b := dnsBackend(backendUUID, "www.example.com", backend.DNS_V4_ONLY)
		return input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), b)
	},
	"udp": func() as3.Input {
		return input(newVIP(vipUUID, "10.99.0.1", vsUUID), udpVS(vsUUID, 53, backendUUID), staticBackend(backendUUID))
	},
	"http": func() as3.Input {
		tlsBackend := staticBackend("66666666-6666-6666-6666-666666666666")
		tlsBackend.EnableTls = true
		tlsBackend.Sni = "api.internal"
		tlsBackend.HealthCheck.HealthChecker = &core.HealthCheck_HttpHealthCheck{HttpHealthCheck: &core.HttpHealthCheck{
			Path: "/health", Host: "api.internal",
		}}
		regex := &route.Route{Name: "items", Action: &route.Route_BackendUuid{BackendUuid: tlsBackend.GetUUID()},
			Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_SafeRegex{SafeRegex: `/items/[0-9]+`}}}
		redirect := &route.Route{Name: "old", Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Path{Path: "/old"}},
			Action: &route.Route_Redirect{Redirect: &route.RedirectAction{PathRedirect: "/new"}}}
		app := vhost([]string{"a.example.com", "*.example.com"}, regex, redirect, prefixRoute("/", backendUUID))
		app.TlsOptions = []*core.TLS{{CertificatesUuid: []string{certUUID}}}
		virtualServer := httpVS(vsUUID, 443, app, vhost([]string{"*"}, prefixRoute("/", backendUUID)))
		in := httpInput(virtualServer, testCert(certUUID))
		in.Backends[tlsBackend.GetUUID()] = tlsBackend
		return in
	},
	"external-auth": func() as3.Input {
		authBackend := staticBackend(authBackendUUID)
		authBackend.EnableTls = true
		authBackend.Sni = "auth.internal"
		open := prefixRoute("/oauth2/", backendUUID)
		open.ExternalAuth = &route.Route_ExternalAuthDisabled{ExternalAuthDisabled: true}
		api := prefixRoute("/api/", backendUUID)
		api.ExternalAuth = &route.Route_ExternalAuthName{ExternalAuthName: "api-keys"}
		app := vhost([]string{"app.example.com"}, open, api, prefixRoute("/", backendUUID))
		app.ExternalAuth = "sso"
		virtualServer := httpVS(vsUUID, 80, app, vhost([]string{"*"}, prefixRoute("/", backendUUID)))
		sso := authPolicy("sso", "x-auth-request-user", "x-auth-request-email")
		apiKeys := authPolicy("api-keys", "x-api-client")
		apiKeys.Path = "/verify"
		apiKeys.FailureMode = route.FailureMode_ALLOW
		virtualServer.GetHttpRoutesConfig().ExternalAuths = []*route.ExternalAuth{sso, apiKeys}
		in := httpInput(virtualServer)
		in.Backends[authBackendUUID] = authBackend
		return in
	},
	"tls": func() as3.Input {
		virtualServer := tlsVS(vsUUID,
			terminate(backendUUID, "a.example.com", "*.example.com"),
			passthrough(otherBackendUUID, "db.example.com"),
			passthrough(otherBackendUUID),
		)
		virtualServer.GetTlsTcpRoutesConfig().AcceptProxyProtocol = true
		in := tlsInput(virtualServer)
		in.Backends[otherBackendUUID].EnableProxyProtocol = true
		return in
	},
	"options": func() as3.Input {
		virtualServer := tcpVS(vsUUID, 443, backendUUID)
		virtualServer.GetTcpRouteConfig().TypedOptions = mustAny(&f5_vsopts.VirtualServerOptions{
			PersistenceProfile:         "/Common/source_addr",
			FallbackPersistenceProfile: "/Common/dest_addr",
			ClientTlsProfile:           "/Common/clientssl",
			ServerTlsProfile:           "/Common/serverssl",
			TcpProfile:                 "/Common/f5-tcp-progressive",
			Snat:                       &f5_vsopts.VirtualServerOptions_None{None: true},
		})
		virtualServer.LimitConfig = []*limit.Config{
			{Action: &limit.Config_ConnectionLimit{ConnectionLimit: &limit.ConnectionLimit{MaxConnections: 1000}}},
			{Action: &limit.Config_ConnectionRateLimit{ConnectionRateLimit: &limit.ConnectionRateLimit{
				Rate: 600, Burst: 10, Unit: flocktype.TimeUnit_MINUTE,
			}}},
		}
		return input(newVIP(vipUUID, "10.99.0.1", vsUUID), virtualServer, staticBackend(backendUUID))
	},
}

var _ = Describe("Golden declarations", func() {
	for name, scenario := range goldenScenarios {
		It("renders the "+name+" scenario", func() {
			doc, err := as3.Document("inst-1", "flock", as3.Render(scenario()).Tenant, true)
			Expect(err).ToNot(HaveOccurred())
			var indented bytes.Buffer
			Expect(json.Indent(&indented, doc, "", "  ")).To(Succeed())
			indented.WriteString("\n")

			path := filepath.Join("testdata", name+".json")
			if *updateGolden {
				Expect(os.MkdirAll("testdata", 0755)).To(Succeed())
				Expect(os.WriteFile(path, indented.Bytes(), 0644)).To(Succeed())
			}
			golden, err := os.ReadFile(path)
			Expect(err).ToNot(HaveOccurred(), "missing golden file, run the suite with -- -update")
			Expect(indented.String()).To(Equal(string(golden)))
		})
	}
})
