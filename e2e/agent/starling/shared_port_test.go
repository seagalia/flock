//go:build e2e

package e2e_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/helpers"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

// An https virtual server and two tls ones (terminated and passthrough) on port 443 of one vip: the agent serves
// them with one envoy listener holding the filter chains of all three, picked on the SNI.
var _ = Describe("shared port: https and tls on 443", Ordered, func() {
	const (
		vipIP      = "10.250.0.200"
		httpsbinIP = "172.31.250.23"
		certUuid   = "e2e-shared"
	)

	BeforeAll(func(ctx SpecContext) {
		cert, key, err := e2eEnv.Certs.LeafCert("backend-b", "git.e2e.local")
		Expect(err).ToNot(HaveOccurred())
		_, err = e2eEnv.StartHttpsHttpbin(ctx, "httpsbin-shared", httpsbinIP, cert, key)
		Expect(err).ToNot(HaveOccurred())
	})

	BeforeEach(func(ctx SpecContext) {
		cert, key, err := e2eEnv.Certs.LeafCert("flock-shared", "web.e2e.local", "db.e2e.local")
		Expect(err).ToNot(HaveOccurred())
		e2eEnv.Controller.Push(mainInstance,
			res.Certificate(certUuid, cert, key),
			res.Backend("e2e-shared-web", []string{httpbin1IP + ":8080"}),
			res.Backend("e2e-shared-db", []string{httpbin1IP + ":8080"}),
			res.Backend("e2e-shared-git", []string{httpsbinIP + ":8080"}),
			res.HttpVs("e2e-shared-https", 443,
				res.HttpRoute(res.VirtualHost([]string{"web.e2e.local"}, res.WithTls(certUuid, "web.e2e.local")), "e2e-shared-web"),
			),
			res.TlsTcpVs("e2e-shared-tls", 443, res.TlsTcpRoute("e2e-shared-db", certUuid, "db.e2e.local")),
			res.TlsTcpVs("e2e-shared-passthrough", 443, res.TlsPassthroughRoute("e2e-shared-git", "git.e2e.local")),
			res.Vip("e2e-shared-vip", vipIP, starling_announce.StarlingAnnounce_STATIC,
				"e2e-shared-https", "e2e-shared-tls", "e2e-shared-passthrough"),
		)
	})

	// httpbin answers /ip once TLS is terminated by flock, either by the https or by the tls virtual server
	answersOn443 := func(ctx SpecContext, host string) func(Gomega) {
		return func(g Gomega) {
			status, body, err := client.HTTPGet(ctx, "https://"+host+"/ip",
				env.WithResolve(host, 443, vipIP), env.WithCA(), env.WithMaxTime(3*time.Second))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(status).To(Equal(200), body)
		}
	}

	It("serves the https virtual server on its name", func(ctx SpecContext) {
		Eventually(answersOn443(ctx, "web.e2e.local")).Should(Succeed())
	})

	It("serves the terminated tls virtual server on its name", func(ctx SpecContext) {
		Eventually(answersOn443(ctx, "db.e2e.local")).Should(Succeed())
	})

	It("passes the connections of the passthrough virtual server through to its endpoints", func(ctx SpecContext) {
		Eventually(func() (string, error) { return client.PeerCertCN(ctx, vipIP, 443, "git.e2e.local") }).Should(Equal("backend-b"))
	})

	It("keeps serving the others when one virtual server leaves the port", func(ctx SpecContext) {
		Eventually(answersOn443(ctx, "web.e2e.local")).Should(Succeed())

		e2eEnv.Controller.Push(mainInstance, res.Vip("e2e-shared-vip", vipIP, starling_announce.StarlingAnnounce_STATIC,
			"e2e-shared-tls", "e2e-shared-passthrough"))
		e2eEnv.Controller.Remove(mainInstance, helpers.TypeUrl[*vs.VirtualServer](), "e2e-shared-https")

		Eventually(func(g Gomega) {
			_, _, err := client.HTTPGet(ctx, "https://web.e2e.local/ip",
				env.WithResolve("web.e2e.local", 443, vipIP), env.WithCA(), env.WithMaxTime(3*time.Second))
			g.Expect(err).To(HaveOccurred())
		}).Should(Succeed())
		Consistently(answersOn443(ctx, "db.e2e.local"), 3*time.Second).Should(Succeed())
		Eventually(func() (string, error) { return client.PeerCertCN(ctx, vipIP, 443, "git.e2e.local") }).Should(Equal("backend-b"))
	})
})
