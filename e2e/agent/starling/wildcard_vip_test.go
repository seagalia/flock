//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("Wildcard VIP", func() {
	const addedIP = "172.31.250.200"

	It("follows the addresses of the agent added and removed while it runs", func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-wildcard", []string{httpbin1IP + ":8080"}),
			res.TcpVs("vs-wildcard", 8090, "c-wildcard"),
			res.Vip("vip-wildcard", "0.0.0.0", starling_announce.StarlingAnnounce_STATIC, "vs-wildcard"),
		)
		addrs := func() []string {
			services := main.Ipvs(ctx)
			result := make([]string, 0, len(services))
			for _, svc := range services {
				result = append(result, svc.Addr)
			}
			return result
		}
		Eventually(addrs).Should(ConsistOf(mainIP + ":8090"))

		Expect(main.Exec(ctx, "ip", "addr", "add", addedIP+"/24", "dev", "eth0")).Error().ToNot(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) { _, _ = main.Exec(ctx, "ip", "addr", "del", addedIP+"/24", "dev", "eth0") })
		Eventually(addrs).Should(ConsistOf(mainIP+":8090", addedIP+":8090"))

		Expect(main.Exec(ctx, "ip", "addr", "del", addedIP+"/24", "dev", "eth0")).Error().ToNot(HaveOccurred())
		Eventually(addrs).Should(ConsistOf(mainIP + ":8090"))
	})
})
