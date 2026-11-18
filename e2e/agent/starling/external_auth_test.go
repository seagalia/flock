//go:build e2e

package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

// upstreamHeaders reads the request headers httpbin echoes back
func upstreamHeaders(body string) http.Header {
	var echo struct {
		Headers http.Header `json:"headers"`
	}
	Expect(json.Unmarshal([]byte(body), &echo)).To(Succeed(), body)
	return echo.Headers
}

var _ = Describe("External auth", Ordered, func() {
	const (
		authStubIP  = "172.31.250.24"
		silentTcpIP = "172.31.250.25"
		vipIP       = "10.250.0.220"
		url         = "http://" + vipIP
		tlsHost     = "auth.e2e.local"
		tlsURL      = "https://" + tlsHost
		tlsCertUuid = "cert-auth"
		// a check that times out takes the policy timeout, a refused connection to the silent backend fails at once
		checkTimeout = 500 * time.Millisecond
		minTimedOut  = 400 * time.Millisecond
		maxTimedOut  = 2 * time.Second
	)

	BeforeAll(func(ctx SpecContext) {
		_, err := e2eEnv.StartAuthStub(ctx, authStubIP)
		Expect(err).ToNot(HaveOccurred())
		_, err = e2eEnv.StartSilentTcp(ctx, silentTcpIP)
		Expect(err).ToNot(HaveOccurred())
	})

	BeforeEach(func(ctx SpecContext) {
		silentPolicy := func(name string, failureMode route.FailureMode) *route.ExternalAuth {
			return &route.ExternalAuth{
				Name:        name,
				BackendUuid: "c-silent",
				Path:        "/check",
				Timeout:     durationpb.New(checkTimeout),
				FailureMode: failureMode,
			}
		}
		policies := res.WithExternalAuths(
			&route.ExternalAuth{
				Name:            "sso",
				BackendUuid:     "c-auth",
				Path:            "/check",
				RequestHeaders:  []string{"x-test-auth", "cookie"},
				UpstreamHeaders: []string{"x-auth-request-user", "x-seen-uri", "x-seen-method", "x-seen-proto", "x-seen-for"},
				ClientHeaders:   []string{"set-cookie"},
			},
			silentPolicy("slow-deny", route.FailureMode_DENY),
			silentPolicy("slow-allow", route.FailureMode_ALLOW),
			&route.ExternalAuth{
				Name:           "error-deny",
				BackendUuid:    "c-auth",
				Path:           "/check",
				RequestHeaders: []string{"x-test-auth"},
				StatusOnError:  502,
			},
		)
		vhost := res.VirtualHost([]string{"*"},
			res.WithVhostExternalAuth("sso"),
			res.WithRouteExternalAuthDisabled("open", "/anything/open/", "c-app"),
			res.WithRouteExternalAuth("slow-deny", "/anything/slow-deny/", "c-app", "slow-deny"),
			res.WithRouteExternalAuth("slow-allow", "/anything/slow-allow/", "c-app", "slow-allow"),
			res.WithRouteExternalAuth("errors", "/anything/errors/", "c-app", "error-deny"),
		)
		tlsCert, tlsKey, err := e2eEnv.Certs.LeafCert("auth", tlsHost)
		Expect(err).ToNot(HaveOccurred())
		tlsVhost := res.VirtualHost([]string{tlsHost}, res.WithVhostExternalAuth("sso"), res.WithTls(tlsCertUuid, tlsHost))
		tlsVhost.TlsOptions[0].AlpnProtocols = []string{"h2", "http/1.1"}
		e2eEnv.Controller.Push(mainInstance,
			res.Certificate(tlsCertUuid, tlsCert, tlsKey),
			res.Backend("c-app", []string{httpbin1IP + ":8080"}),
			res.Backend("c-auth", []string{authStubIP + ":8090"}),
			res.Backend("c-silent", []string{silentTcpIP + ":8091"}),
			res.Apply(res.HttpVs("vs-auth", 80, res.HttpRoute(vhost, "c-app")), policies),
			res.Apply(res.HttpVs("vs-auth-tls", 443, res.HttpRoute(tlsVhost, "c-app")), policies),
			res.Vip("vip-auth", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-auth", "vs-auth-tls"),
		)
		// an allowed check proves the listener and the cluster of the auth stub are both ready
		Eventually(func() (int, error) {
			status, _, err := client.HTTPGet(ctx, url+"/headers", env.WithHeader("x-test-auth: allow"))
			return status, err
		}).Should(Equal(200))
		// a check on c-silent with no host yet fails at once, which the timeout specs would take for the policy timeout
		Eventually(func() []string { return main.EnvoyClusterHosts(ctx, "c-silent") }).Should(ConsistOf(silentTcpIP + ":8091"))
	})

	It("lets an allowed request through with the upstream headers", func(ctx SpecContext) {
		status, _, body, err := client.HTTPGetHeaders(ctx, url+"/headers?x=1", env.WithHeader("x-test-auth: allow"))
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(200))
		headers := upstreamHeaders(body)
		Expect(headers.Values("X-Auth-Request-User")).To(Equal([]string{"alice"}))
		Expect(headers.Values("X-Seen-Uri")).To(Equal([]string{"/headers?x=1"}))
		Expect(headers.Values("X-Seen-Method")).To(Equal([]string{"GET"}))
	})

	It("tells the check the scheme of the client connection, whatever the client sent", func(ctx SpecContext) {
		allow := env.WithHeader("x-test-auth: allow")
		status, _, body, err := client.HTTPGetHeaders(ctx, url+"/headers", allow)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(200))
		Expect(upstreamHeaders(body).Values("X-Seen-Proto")).To(Equal([]string{"http"}))

		status, _, body, err = client.HTTPGetHeaders(ctx, url+"/headers", allow, env.WithHeader("x-forwarded-proto: https"))
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(200))
		Expect(upstreamHeaders(body).Values("X-Seen-Proto")).To(Equal([]string{"http"}))
	})

	It("tells the check https on a TLS virtual host, over HTTP/2 too", func(ctx SpecContext) {
		allow := env.WithHeader("x-test-auth: allow")
		overTLS := []env.CurlOpt{env.WithResolve(tlsHost, 443, vipIP), env.WithCA()}
		Eventually(func() (int, error) {
			status, _, err := client.HTTPGet(ctx, tlsURL+"/headers", append(overTLS, allow)...)
			return status, err
		}).Should(Equal(200))

		status, _, body, err := client.HTTPGetHeaders(ctx, tlsURL+"/headers", append(overTLS, allow)...)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(200))
		Expect(upstreamHeaders(body).Values("X-Seen-Proto")).To(Equal([]string{"https"}))

		http2 := append(overTLS, env.WithHttp2())
		versionArgs := []string{"curl", "-s", "-o", "/dev/null", "-w", "%{http_version}"}
		for _, opt := range append(http2, allow) {
			versionArgs = append(versionArgs, opt()...)
		}
		version, err := client.Exec(ctx, append(versionArgs, tlsURL+"/headers")...)
		Expect(err).ToNot(HaveOccurred())
		Expect(strings.TrimSpace(version)).To(Equal("2"))
		status, _, body, err = client.HTTPGetHeaders(ctx, tlsURL+"/headers",
			append(http2, allow, env.WithHeader("x-forwarded-proto: http"))...)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(200))
		Expect(upstreamHeaders(body).Values("X-Seen-Proto")).To(Equal([]string{"https"}))
	})

	It("tells the check the client address alone as x-forwarded-for", func(ctx SpecContext) {
		status, _, body, err := client.HTTPGetHeaders(ctx, url+"/headers",
			env.WithHeader("x-test-auth: allow"), env.WithHeader("X-Forwarded-For: 6.6.6.6"))
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(200))
		Expect(upstreamHeaders(body).Values("X-Seen-For")).To(Equal([]string{clientIP}))
	})

	It("leaves the forwarded headers of the check out of the backend request", func(ctx SpecContext) {
		status, _, body, err := client.HTTPGetHeaders(ctx, url+"/headers",
			env.WithHeader("x-test-auth: allow"), env.WithHeader("X-Forwarded-Host: client.example"))
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(200))
		headers := upstreamHeaders(body)
		Expect(headers.Values("X-Forwarded-Method")).To(BeEmpty())
		Expect(headers.Values("X-Forwarded-Uri")).To(BeEmpty())
		Expect(headers.Values("X-Forwarded-Host")).To(Equal([]string{"client.example"}))
	})

	It("passes a 401 with its body and www-authenticate", func(ctx SpecContext) {
		status, headers, body, err := client.HTTPGetHeaders(ctx, url+"/headers", env.WithHeader("x-test-auth: deny"))
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(401))
		Expect(body).To(Equal("denied"))
		Expect(headers.Get("www-authenticate")).To(Equal("Bearer"))
	})

	It("passes a 302 with location and both cookies only", func(ctx SpecContext) {
		status, headers, _, err := client.HTTPGetHeaders(ctx, url+"/headers", env.WithHeader("x-test-auth: redirect"))
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(302))
		Expect(headers.Get("location")).To(Equal("http://login.example/"))
		Expect(headers.Values("set-cookie")).To(ConsistOf("a=1", "b=2"))
		Expect(headers.Values("x-hidden")).To(BeEmpty())
	})

	It("passes a 202 to the client without reaching the backend", func(ctx SpecContext) {
		status, _, body, err := client.HTTPGetHeaders(ctx, url+"/headers", env.WithHeader("x-test-auth: accepted"))
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(202))
		Expect(body).ToNot(ContainSubstring(`"headers"`), "the backend answered")
	})

	It("answers status_on_error with an empty body on a 5xx", func(ctx SpecContext) {
		status, _, body, err := client.HTTPGetHeaders(ctx, url+"/anything/errors/", env.WithHeader("x-test-auth: error"))
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(502))
		Expect(body).To(BeEmpty())
	})

	It("denies on timeout with failure mode DENY", func(ctx SpecContext) {
		sentAt := time.Now()
		status, _, body, err := client.HTTPGetHeaders(ctx, url+"/anything/slow-deny/")
		elapsed := time.Since(sentAt)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(503))
		Expect(body).To(BeEmpty())
		Expect(elapsed).To(And(BeNumerically(">=", minTimedOut), BeNumerically("<", maxTimedOut)))
	})

	It("lets the request through on timeout with failure mode ALLOW", func(ctx SpecContext) {
		sentAt := time.Now()
		status, _, body, err := client.HTTPGetHeaders(ctx, url+"/anything/slow-allow/")
		elapsed := time.Since(sentAt)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(200))
		Expect(elapsed).To(And(BeNumerically(">=", minTimedOut), BeNumerically("<", maxTimedOut)))
		Expect(upstreamHeaders(body).Values("X-Auth-Request-User")).To(BeEmpty())
	})

	It("strips a spoofed upstream header", func(ctx SpecContext) {
		spoofed := env.WithHeader("x-auth-request-user: mallory")

		status, _, body, err := client.HTTPGetHeaders(ctx, url+"/headers", spoofed, env.WithHeader("x-test-auth: allow"))
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(200))
		Expect(upstreamHeaders(body).Values("X-Auth-Request-User")).To(Equal([]string{"alice"}))

		status, _, body, err = client.HTTPGetHeaders(ctx, url+"/anything/open/", spoofed)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(200))
		Expect(body).ToNot(ContainSubstring("mallory"))
	})

	It("checks the paths outside the disabled route with the default of the virtual host", func(ctx SpecContext) {
		status, _, _, err := client.HTTPGetHeaders(ctx, url+"/headers")
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(403))
	})
})

var _ = Describe("External auth with oauth2-proxy", Ordered, func() {
	const (
		mockOidcIP    = "172.31.250.26"
		oauth2ProxyIP = "172.31.250.27"
		vipIP         = "10.250.0.221"
		url           = "http://" + vipIP
		issuer        = "http://" + mockOidcIP + ":8080/default"
	)
	browser := env.WithHeader("accept: text/html")

	BeforeAll(func(ctx SpecContext) {
		_, err := e2eEnv.StartMockOidc(ctx, mockOidcIP)
		Expect(err).ToNot(HaveOccurred())
		_, err = e2eEnv.StartOauth2Proxy(ctx, oauth2ProxyIP, issuer, vipIP)
		Expect(err).ToNot(HaveOccurred())
	})

	BeforeEach(func(ctx SpecContext) {
		policies := res.WithExternalAuths(&route.ExternalAuth{
			Name:            "sso",
			BackendUuid:     "b-oauth2",
			Path:            "/",
			RequestHeaders:  []string{"cookie", "accept"},
			UpstreamHeaders: []string{"x-auth-request-user", "x-auth-request-email"},
			ClientHeaders:   []string{"set-cookie"},
		})
		vhost := res.VirtualHost([]string{"*"},
			res.WithVhostExternalAuth("sso"),
			res.WithRouteExternalAuthDisabled("oauth2", "/oauth2/", "b-oauth2"),
		)
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("b-app", []string{httpbin1IP + ":8080"}),
			res.Backend("b-oauth2", []string{oauth2ProxyIP + ":4180"}),
			res.Apply(res.HttpVs("vs-oauth2", 80, res.HttpRoute(vhost, "b-app")), policies),
			res.Vip("vip-oauth2", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-oauth2"),
		)
		// a redirect comes from oauth2-proxy, so the listener and its cluster are both ready
		Eventually(func() (int, error) {
			status, _, err := client.Redirect(ctx, url+"/headers", browser)
			return status, err
		}).Should(Equal(302))
	})

	It("redirects an anonymous browser to the provider", func(ctx SpecContext) {
		status, location, err := client.Redirect(ctx, url+"/headers", browser)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(302))
		Expect(location).To(HavePrefix(issuer + "/authorize"))
	})

	It("reaches the backend once logged in", func(ctx SpecContext) {
		status, body, err := client.Login(ctx, url+"/headers", browser)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(200), body)
		headers := upstreamHeaders(body)
		Expect(headers.Values("X-Auth-Request-User")).To(Equal([]string{env.MockOidcUser}))
		Expect(headers.Values("X-Auth-Request-Email")).To(Equal([]string{env.MockOidcEmail}))
	})

	It("serves the oauth2-proxy routes unchecked", func(ctx SpecContext) {
		// oauth2-proxy answers /oauth2/auth 401 to an anonymous request, a check would have redirected it
		status, _, err := client.Redirect(ctx, url+"/oauth2/auth", browser)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(401))
	})

	It("replaces a spoofed identity by the real one", func(ctx SpecContext) {
		status, body, err := client.Login(ctx, url+"/headers", browser, env.WithHeader("x-auth-request-user: mallory"))
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(200), body)
		Expect(upstreamHeaders(body).Values("X-Auth-Request-User")).To(Equal([]string{env.MockOidcUser}))
		Expect(body).ToNot(ContainSubstring("mallory"))
	})
})
