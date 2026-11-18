//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/helpers"

	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("ARP", func() {
	// vip in the network subnet: the client has no route for it and must resolve it with arp
	const vipIP = "172.31.250.210"

	BeforeEach(func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-arp", []string{httpbin1IP + ":8080", httpbin2IP + ":8080"}),
			res.TcpVs("vs-arp", 80, "c-arp"),
			res.Vip("vip-arp", vipIP, starling_announce.StarlingAnnounce_LAYER2, "vs-arp"),
		)
	})

	It("answers arp for the vip and serves it", func(ctx SpecContext) {
		mainMAC := main.MAC(ctx)
		Eventually(func() (string, error) { return client.Arping(ctx, vipIP) }).Should(Equal(mainMAC))
		Eventually(func() (string, error) { return client.Hostname(ctx, "http://"+vipIP+"/hostname") }).ShouldNot(BeEmpty())
		Expect(client.Neigh(ctx, vipIP)).To(Equal(mainMAC))
	})

	It("stops answering arp once the vip is removed", func(ctx SpecContext) {
		Eventually(func() (string, error) { return client.Arping(ctx, vipIP) }).Should(Equal(main.MAC(ctx)))
		e2eEnv.Controller.Remove(mainInstance, helpers.TypeUrl[*vip.VIP](), "vip-arp")
		Eventually(func() error {
			_, err := client.Arping(ctx, vipIP)
			return err
		}).Should(HaveOccurred())
	})
})
