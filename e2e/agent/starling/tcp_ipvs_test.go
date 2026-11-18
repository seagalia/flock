//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/helpers"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

func distinctHostnames(ctx SpecContext, url string, n int, opts ...env.CurlOpt) []string {
	seen := map[string]bool{}
	for range n {
		hostname, err := client.Hostname(ctx, url, opts...)
		if err == nil {
			seen[hostname] = true
		}
	}
	result := make([]string, 0)
	for hostname := range seen {
		result = append(result, hostname)
	}
	return result
}

var _ = Describe("TCP through IPVS", func() {
	const vipIP = "10.250.0.10"

	BeforeEach(func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-tcp", []string{httpbin1IP + ":8080", httpbin2IP + ":8080"}),
			res.TcpVs("vs-tcp", 80, "c-tcp"),
			res.Vip("vip-tcp", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-tcp"),
		)
	})

	It("programs route, ipvs and snat", func(ctx SpecContext) {
		Eventually(func() []string { return main.LocalRoutes(ctx) }).Should(ContainElement(vipIP + "/32"))
		Eventually(func() []env.IpvsService { return main.Ipvs(ctx) }).Should(ConsistOf(env.IpvsService{
			Proto: "TCP", Addr: vipIP + ":80", Scheduler: "rr",
			Dests: []env.IpvsDest{
				{Addr: httpbin1IP + ":8080", Fwd: "Masq", Weight: 1},
				{Addr: httpbin2IP + ":8080", Fwd: "Masq", Weight: 1},
			},
		}))
		Expect(main.FlockNftables(ctx)).To(ContainElement(ContainSubstring(vipIP + " . 80 : jump")))
	})

	It("balances client traffic on both backends", func(ctx SpecContext) {
		Eventually(func() []string {
			return distinctHostnames(ctx, "http://"+vipIP+"/hostname", 20)
		}).Should(ConsistOf("httpbin-1", "httpbin-2"))
	})

	It("cleans ipvs and snat when the virtual server is removed", func(ctx SpecContext) {
		Eventually(func() []env.IpvsService { return main.Ipvs(ctx) }).Should(HaveLen(1))

		e2eEnv.Controller.Remove(mainInstance, helpers.TypeUrl[*vs.VirtualServer](), "vs-tcp")
		Eventually(func() []env.IpvsService { return main.Ipvs(ctx) }).Should(BeEmpty())
		Eventually(func() []string { return main.FlockNftables(ctx) }).ShouldNot(ContainElement(ContainSubstring(vipIP)))
		Expect(main.LocalRoutes(ctx)).To(ContainElement(vipIP+"/32"), "vip is still there")

		e2eEnv.Controller.Remove(mainInstance, helpers.TypeUrl[*vip.VIP](), "vip-tcp")
		Eventually(func() []string { return main.LocalRoutes(ctx) }).ShouldNot(ContainElement(vipIP + "/32"))
	})
})
