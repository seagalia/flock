//go:build e2e

package e2e_test

import (
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

// A hand-made SNI front, the setup accept_proxy_protocol exists for: a passthrough virtual server on 443 sends each
// name to an inner virtual server of the same vip on an internal port, with proxy protocol for the "-pp" inner
// virtual servers so they still see the real client.
var _ = Describe("SNI front: https and tls on one port", Ordered, func() {
	const (
		vipIP       = "10.250.0.62"
		httpsbinIP  = "172.31.250.18"
		certUuid    = "e2e-front"
		httpsPort   = 19500
		httpsPpPort = 19501
		tlsPpPort   = 19502
	)
	internal := func(port int) string { return vipIP + ":" + strconv.Itoa(port) }

	BeforeAll(func(ctx SpecContext) {
		cert, key, err := e2eEnv.Certs.LeafCert("backend-a", "git.e2e.local")
		Expect(err).ToNot(HaveOccurred())
		_, err = e2eEnv.StartHttpsHttpbin(ctx, "httpsbin-git", httpsbinIP, cert, key)
		Expect(err).ToNot(HaveOccurred())
	})

	BeforeEach(func(ctx SpecContext) {
		cert, key, err := e2eEnv.Certs.LeafCert("flock-front", "web.e2e.local", "web-pp.e2e.local", "db-pp.e2e.local")
		Expect(err).ToNot(HaveOccurred())
		e2eEnv.Controller.Push(mainInstance,
			res.Certificate(certUuid, cert, key),
			res.Backend("e2e-web", []string{httpbin1IP + ":8080"}),
			res.Backend("e2e-web-pp", []string{httpbin1IP + ":8080"}),
			res.Backend("e2e-db-pp", []string{ppBackendIP + ":8081"}, res.WithProxyProtocolV2()),
			res.Backend("e2e-git", []string{httpsbinIP + ":8080"}),
			res.Backend("e2e-sni-443-web", []string{internal(httpsPort)}),
			res.Backend("e2e-sni-443-web-pp", []string{internal(httpsPpPort)}, res.WithProxyProtocolV2()),
			res.Backend("e2e-sni-443-db-pp", []string{internal(tlsPpPort)}, res.WithProxyProtocolV2()),
			res.TlsTcpVs("e2e-sni-443", 443,
				res.TlsPassthroughRoute("e2e-sni-443-web", "web.e2e.local"),
				res.TlsPassthroughRoute("e2e-sni-443-web-pp", "web-pp.e2e.local"),
				res.TlsPassthroughRoute("e2e-sni-443-db-pp", "db-pp.e2e.local"),
				res.TlsPassthroughRoute("e2e-git", "git.e2e.local"),
			),
			res.HttpVs("e2e-https-443", httpsPort,
				res.HttpRoute(res.VirtualHost([]string{"web.e2e.local"}, res.WithTls(certUuid, "web.e2e.local")), "e2e-web"),
			),
			res.Apply(res.HttpVs("e2e-https-443-pp", httpsPpPort,
				res.HttpRoute(res.VirtualHost([]string{"web-pp.e2e.local"}, res.WithTls(certUuid, "web-pp.e2e.local")), "e2e-web-pp"),
			), res.WithAcceptProxyProtocol()),
			res.Apply(res.TlsTcpVs("e2e-tls-443-pp", tlsPpPort,
				res.TlsTcpRoute("e2e-db-pp", certUuid, "db-pp.e2e.local"),
			), res.WithAcceptProxyProtocol()),
			res.Vip("e2e-vip", vipIP, starling_announce.StarlingAnnounce_STATIC,
				"e2e-sni-443", "e2e-https-443", "e2e-https-443-pp", "e2e-tls-443-pp"),
		)
	})

	// httpbin answers /ip with the first address of X-Forwarded-For, which envoy fills with the client it saw
	clientSeenBy := func(ctx SpecContext, host string) func(Gomega) string {
		return func(g Gomega) string {
			status, body, err := client.HTTPGet(ctx, "https://"+host+"/ip", env.WithResolve(host, 443, vipIP), env.WithCA())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(status).To(Equal(200), body)
			return body
		}
	}

	It("gives the real client to an https service asking for it", func(ctx SpecContext) {
		Eventually(clientSeenBy(ctx, "web-pp.e2e.local")).Should(ContainSubstring(`"` + clientIP + `"`))
	})

	It("shows the vip as client to an https service not asking for it", func(ctx SpecContext) {
		Eventually(clientSeenBy(ctx, "web.e2e.local")).Should(And(
			ContainSubstring(`"`+vipIP+`"`),
			Not(ContainSubstring(clientIP)),
		))
	})

	It("gives the real client to the endpoints of a tls service asking for it", func(ctx SpecContext) {
		Eventually(func(g Gomega) {
			status, body, err := client.HTTPGet(ctx, "https://db-pp.e2e.local/pp-client",
				env.WithResolve("db-pp.e2e.local", 443, vipIP), env.WithCA())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(status).To(Equal(200), body)
			g.Expect(body).To(Equal(clientIP))
		}).Should(Succeed())
	})

	It("reaches a passthrough tls service through the front", func(ctx SpecContext) {
		Eventually(func() (string, error) { return client.PeerCertCN(ctx, vipIP, 443, "git.e2e.local") }).Should(Equal("backend-a"))
	})

	It("refuses a connection without proxy protocol on a -pp inner virtual server", func(ctx SpecContext) {
		// the same inner virtual server answers through the front: the refusal is not a missing listener
		Eventually(clientSeenBy(ctx, "web-pp.e2e.local")).Should(ContainSubstring(clientIP))
		_, body, err := client.HTTPGet(ctx, "https://web-pp.e2e.local:"+strconv.Itoa(httpsPpPort)+"/ip",
			env.WithResolve("web-pp.e2e.local", httpsPpPort, vipIP), env.WithCA(), env.WithMaxTime(3*time.Second))
		Expect(err).To(HaveOccurred(), body)
	})
})
