//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("UDP through IPVS", func() {
	const vipIP = "10.250.0.20"

	BeforeEach(func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-udp", []string{udpEcho1IP + ":9000", udpEcho2IP + ":9000"}, res.WithNoHealthCheck()),
			res.UdpVs("vs-udp", 53, "c-udp"),
			res.Vip("vip-udp", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-udp"),
		)
	})

	It("balances datagrams on both backends", func(ctx SpecContext) {
		Eventually(func() []env.IpvsService { return main.Ipvs(ctx) }).Should(ContainElement(And(
			HaveField("Proto", "UDP"), HaveField("Addr", vipIP+":53"), HaveField("Dests", HaveLen(2)),
		)))
		Eventually(func() map[string]bool {
			seen := map[string]bool{}
			for range 20 {
				answer, err := client.UDP(ctx, vipIP+":53", "ping")
				if err == nil && answer != "" {
					seen[answer] = true
				}
			}
			return seen
		}).Should(Equal(map[string]bool{"udp-echo-1": true, "udp-echo-2": true}))
	})
})
