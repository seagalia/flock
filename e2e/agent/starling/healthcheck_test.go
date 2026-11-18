//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("Health checks", Ordered, func() {
	backends := []string{httpbin1IP + ":8080", httpbin2IP + ":8080"}

	// httpbin-1 is shared with other scenarios, it must be running again whatever happens here
	AfterAll(func(ctx SpecContext) {
		Expect(e2eEnv.StartContainer(ctx, httpbin1)).To(Succeed())
		Eventually(func() (string, error) {
			return client.Hostname(ctx, "http://"+httpbin1IP+":8080/hostname")
		}).Should(Equal("httpbin-1"))
	})

	It("removes an unhealthy backend from ipvs and adds it back once healthy", func(ctx SpecContext) {
		const vipIP = "10.250.0.60"
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-hc", backends),
			res.TcpVs("vs-hc", 80, "c-hc"),
			res.Vip("vip-hc", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-hc"),
		)
		ipvsDests := func() []string {
			dests := make([]string, 0)
			for _, svc := range main.Ipvs(ctx) {
				for _, dest := range svc.Dests {
					dests = append(dests, dest.Addr)
				}
			}
			return dests
		}
		Eventually(ipvsDests).Should(ConsistOf(backends))

		Expect(e2eEnv.StopContainer(ctx, httpbin1)).To(Succeed())
		Eventually(ipvsDests).Should(ConsistOf(httpbin2IP + ":8080"))
		Expect(distinctHostnames(ctx, "http://"+vipIP+"/hostname", 10)).To(ConsistOf("httpbin-2"))

		Expect(e2eEnv.StartContainer(ctx, httpbin1)).To(Succeed())
		Eventually(ipvsDests).Should(ConsistOf(backends))
	})

	It("removes an unhealthy backend from envoy and adds it back once healthy", func(ctx SpecContext) {
		const vipIP = "10.250.0.61"
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-hc-envoy", backends),
			res.HttpVs("vs-hc-envoy", 80, res.HttpRoute(res.VirtualHost([]string{"*"}), "c-hc-envoy")),
			res.Vip("vip-hc-envoy", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-hc-envoy"),
		)
		envoyHosts := func() []string { return main.EnvoyClusterHosts(ctx, "c-hc-envoy") }
		Eventually(envoyHosts).Should(ConsistOf(backends))

		Expect(e2eEnv.StopContainer(ctx, httpbin1)).To(Succeed())
		Eventually(envoyHosts).Should(ConsistOf(httpbin2IP + ":8080"))
		Eventually(func() []string {
			return distinctHostnames(ctx, "http://"+vipIP+"/hostname", 10)
		}).Should(ConsistOf("httpbin-2"))

		Expect(e2eEnv.StartContainer(ctx, httpbin1)).To(Succeed())
		Eventually(envoyHosts).Should(ConsistOf(backends))
	})
})
