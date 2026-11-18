//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("TLS with SNI routing", func() {
	const (
		certAUuid = "cert-a"
		certBUuid = "cert-b"
	)
	var certA, keyA, certB, keyB []byte

	BeforeEach(func(ctx SpecContext) {
		var err error
		certA, keyA, err = e2eEnv.Certs.LeafCert("a", "a.e2e.local")
		Expect(err).ToNot(HaveOccurred())
		certB, keyB, err = e2eEnv.Certs.LeafCert("b", "b.e2e.local")
		Expect(err).ToNot(HaveOccurred())
		e2eEnv.Controller.Push(mainInstance,
			res.Certificate(certAUuid, certA, keyA),
			res.Certificate(certBUuid, certB, keyB),
			res.Backend("c-a", []string{httpbin1IP + ":8080"}),
			res.Backend("c-b", []string{httpbin2IP + ":8080"}),
		)
	})

	sniBehaviour := func(currentVip func() string) {
		It("serves the certificate and backend of each name", func(ctx SpecContext) {
			vipIP := currentVip()
			for name, expected := range map[string][2]string{"a": {"a", "httpbin-1"}, "b": {"b", "httpbin-2"}} {
				host := name + ".e2e.local"
				Eventually(func() (string, error) { return client.PeerCertCN(ctx, vipIP, 443, host) }).Should(Equal(expected[0]), host)
				Eventually(func() (string, error) {
					return client.Hostname(ctx, "https://"+host+"/hostname", env.WithResolve(host, 443, vipIP), env.WithCA())
				}).Should(Equal(expected[1]), host)
			}
		})

		It("refuses an unknown name and no name", func(ctx SpecContext) {
			vipIP := currentVip()
			Eventually(func() (string, error) { return client.PeerCertCN(ctx, vipIP, 443, "a.e2e.local") }).Should(Equal("a"))
			_, err := client.PeerCertCN(ctx, vipIP, 443, "c.e2e.local")
			Expect(err).To(HaveOccurred())
			_, err = client.PeerCertCN(ctx, vipIP, 443, "")
			Expect(err).To(HaveOccurred())
		})

		It("rotates a certificate without touching the other", func(ctx SpecContext) {
			vipIP := currentVip()
			Eventually(func() (string, error) { return client.PeerCertCN(ctx, vipIP, 443, "a.e2e.local") }).Should(Equal("a"))
			// envoy may serve one filter chain of a new listener a few milliseconds before the other, so b must be ready
			// before the rotation for the check after it to mean anything
			Eventually(func() (string, error) { return client.PeerCertCN(ctx, vipIP, 443, "b.e2e.local") }).Should(Equal("b"))
			certA2, keyA2, err := e2eEnv.Certs.LeafCert("a2", "a.e2e.local")
			Expect(err).ToNot(HaveOccurred())
			e2eEnv.Controller.Push(mainInstance, res.Certificate(certAUuid, certA2, keyA2))

			Eventually(func() (string, error) { return client.PeerCertCN(ctx, vipIP, 443, "a.e2e.local") }).Should(Equal("a2"))
			Expect(client.PeerCertCN(ctx, vipIP, 443, "b.e2e.local")).To(Equal("b"))
		})
	}

	Context("with https virtual hosts", func() {
		const vipIP = "10.250.0.40"

		BeforeEach(func(ctx SpecContext) {
			e2eEnv.Controller.Push(mainInstance,
				res.HttpVs("vs-https", 443, res.HttpRoute(res.VirtualHost([]string{"a.e2e.local"}, res.WithTls(certAUuid, "a.e2e.local")), "c-a"), res.HttpRoute(res.VirtualHost([]string{"b.e2e.local"}, res.WithTls(certBUuid, "b.e2e.local")), "c-b")),
				res.Vip("vip-https", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-https"),
			)
		})

		sniBehaviour(func() string { return vipIP })
	})

	Context("with tls over tcp", func() {
		const vipIP = "10.250.0.41"

		BeforeEach(func(ctx SpecContext) {
			e2eEnv.Controller.Push(mainInstance,
				res.TlsTcpVs("vs-tlstcp", 443,
					res.TlsTcpRoute("c-a", certAUuid, "a.e2e.local"),
					res.TlsTcpRoute("c-b", certBUuid, "b.e2e.local"),
				),
				res.Vip("vip-tlstcp", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-tlstcp"),
			)
		})

		sniBehaviour(func() string { return vipIP })
	})
})
