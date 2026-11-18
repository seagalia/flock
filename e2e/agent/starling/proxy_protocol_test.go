//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("TCP with proxy protocol", func() {
	const (
		vipIP = "10.250.0.50"
		url   = "http://" + vipIP
	)

	BeforeEach(func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-pp", []string{ppBackendIP + ":8081"}, res.WithProxyProtocolV2()),
			res.TcpVs("vs-pp", 80, "c-pp"),
			res.Vip("vip-pp", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-pp"),
		)
	})

	It("goes through envoy, not ipvs", func(ctx SpecContext) {
		Eventually(func() []string { return main.EnvoyListeners(ctx) }).Should(ContainElement("tcp-80/vip-pp"))
		Consistently(func() []env.IpvsService { return main.Ipvs(ctx) }, "2s").Should(BeEmpty())
	})

	It("sends the client address in the proxy protocol header", func(ctx SpecContext) {
		Eventually(func(g Gomega) {
			status, body, err := client.HTTPGet(ctx, url+"/pp-client")
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(status).To(Equal(200))
			g.Expect(body).To(Equal(clientIP))
		}).Should(Succeed())
	})
})
