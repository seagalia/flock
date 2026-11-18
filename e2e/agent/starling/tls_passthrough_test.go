//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("TLS passthrough with SNI routing", Ordered, func() {
	const (
		vipIP         = "10.250.0.42"
		httpsbin1IP   = "172.31.250.16"
		httpsbin2IP   = "172.31.250.17"
		terminateUuid = "cert-terminate"
	)

	BeforeAll(func(ctx SpecContext) {
		for name, ip := range map[string]string{"a": httpsbin1IP, "b": httpsbin2IP} {
			cert, key, err := e2eEnv.Certs.LeafCert("backend-"+name, name+".e2e.local")
			Expect(err).ToNot(HaveOccurred())
			_, err = e2eEnv.StartHttpsHttpbin(ctx, "httpsbin-"+name, ip, cert, key)
			Expect(err).ToNot(HaveOccurred())
		}
	})

	BeforeEach(func(ctx SpecContext) {
		cert, key, err := e2eEnv.Certs.LeafCert("flock-c", "c.e2e.local")
		Expect(err).ToNot(HaveOccurred())
		e2eEnv.Controller.Push(mainInstance,
			res.Certificate(terminateUuid, cert, key),
			res.Backend("c-tls-a", []string{httpsbin1IP + ":8080"}),
			res.Backend("c-tls-b", []string{httpsbin2IP + ":8080"}),
			res.Backend("c-plain", []string{httpbin1IP + ":8080"}),
			res.TlsTcpVs("vs-passthrough", 443,
				res.TlsPassthroughRoute("c-tls-a", "a.e2e.local"),
				res.TlsPassthroughRoute("c-tls-b", "*.b.e2e.local", "b.e2e.local"),
				res.TlsTcpRoute("c-plain", terminateUuid, "c.e2e.local"),
				// no server names: everything no other route matches, plain tcp included
				res.TlsPassthroughRoute("c-plain"),
			),
			res.Vip("vip-passthrough", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-passthrough"),
		)
	})

	It("serves the certificate and answer of the backend of each name", func(ctx SpecContext) {
		for name, expected := range map[string][2]string{"a": {"backend-a", "httpsbin-a"}, "b": {"backend-b", "httpsbin-b"}} {
			host := name + ".e2e.local"
			Eventually(func() (string, error) { return client.PeerCertCN(ctx, vipIP, 443, host) }).Should(Equal(expected[0]), host)
			Eventually(func() (string, error) {
				return client.Hostname(ctx, "https://"+host+"/hostname", env.WithResolve(host, 443, vipIP), env.WithCA())
			}).Should(Equal(expected[1]), host)
		}
	})

	It("terminates tls on a route with certificates next to passthrough routes", func(ctx SpecContext) {
		Eventually(func() (string, error) { return client.PeerCertCN(ctx, vipIP, 443, "c.e2e.local") }).Should(Equal("flock-c"))
		Eventually(func() (string, error) {
			return client.Hostname(ctx, "https://c.e2e.local/hostname", env.WithResolve("c.e2e.local", 443, vipIP), env.WithCA())
		}).Should(Equal("httpbin-1"))
	})

	It("sends plain tcp to the route without server names", func(ctx SpecContext) {
		Eventually(func() (string, error) {
			return client.Hostname(ctx, "http://"+vipIP+":443/hostname")
		}).Should(Equal("httpbin-1"))
	})
})
