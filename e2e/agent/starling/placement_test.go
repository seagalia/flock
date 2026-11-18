//go:build e2e

package e2e_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("Placement", Ordered, func() {
	const (
		instance = "placement"
		p1IP     = "172.31.250.101"
		p2IP     = "172.31.250.102"
	)
	vips := []string{"172.31.250.201", "172.31.250.202", "172.31.250.203", "172.31.250.204"}
	var p1, p2 *env.Agent

	placementConfig := func(ownIP, otherIP string) func(*config.Config) {
		return func(conf *config.Config) {
			conf.AdvertisementConfig.L2Config.UseDynamicPlacement = true
			conf.AdvertisementConfig.DynamicPlacementStrategy = "distributed"
			conf.AdvertisementConfig.AgentList = &config.AgentListConfig{
				ListenHost: ownIP,
				Members:    []string{otherIP + ":18001"},
				Secret:     "e2e-placement-secret",
			}
		}
	}

	BeforeAll(func(ctx SpecContext) {
		var err error
		p1, err = e2eEnv.StartAgent(ctx, env.AgentOptions{Name: "agent-p1", IP: p1IP, Instance: instance, Configure: placementConfig(p1IP, p2IP)})
		Expect(err).ToNot(HaveOccurred())
		p2, err = e2eEnv.StartAgent(ctx, env.AgentOptions{Name: "agent-p2", IP: p2IP, Instance: instance, Configure: placementConfig(p2IP, p1IP)})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			e2eEnv.Controller.RemoveAll(instance)
			_ = p1.Stop(ctx)
			_ = p2.Stop(ctx)
		})

		for i, ip := range vips {
			e2eEnv.Controller.Push(instance, res.Vip("vip-place-"+string(rune('a'+i)), ip, starling_announce.StarlingAnnounce_LAYER2))
		}
	}, NodeTimeout(5*time.Minute))

	It("announces each vip from exactly one agent", func(ctx SpecContext) {
		var announced1, announced2 []string
		Eventually(func(g Gomega) {
			announced1, announced2 = p1.AnnouncedVips(ctx), p2.AnnouncedVips(ctx)
			g.Expect(announced1).ToNot(BeEmpty())
			g.Expect(announced2).ToNot(BeEmpty())
			g.Expect(append(append([]string{}, announced1...), announced2...)).To(ConsistOf(
				vips[0]+"/32", vips[1]+"/32", vips[2]+"/32", vips[3]+"/32",
			))
		}, "1m").Should(Succeed())

		// the agent owning a vip is the one answering arp for it
		ownedByP1 := announced1[0][:len(announced1[0])-len("/32")]
		Eventually(func() (string, error) { return client.Arping(ctx, ownedByP1) }).Should(Equal(p1.MAC(ctx)))
	})

	It("moves vips to the remaining agent when one leaves", func(ctx SpecContext) {
		Expect(p2.Stop(ctx)).To(Succeed())
		Eventually(func() []string { return p1.AnnouncedVips(ctx) }, "1m").Should(ConsistOf(
			vips[0]+"/32", vips[1]+"/32", vips[2]+"/32", vips[3]+"/32",
		))
	})
})
