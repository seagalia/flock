package agentlist_test

import (
	"context"

	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	. "github.com/seagalia/flock/agent/starling/agentlist"
)

var _ = Describe("VipPlacer", func() {
	var (
		placer   *VipPlacer
		listener *recordingPlacerListener
	)

	BeforeEach(func() {
		listener = &recordingPlacerListener{}
	})

	When("running alone without agent list", func() {
		BeforeEach(func() {
			placer = NewVipPlacer(context.Background(), "node-1", nil, nil, vips)
			placer.AddPlaceOn(starling_announce.StarlingAnnounce_LAYER2)
			placer.AddPlacerListener(listener)
		})
		AfterEach(func() {
			Expect(placer.Stop()).To(Succeed())
		})

		It("should announce all vips which match placed announce type", func() {
			Expect(vips.Create(context.Background(), makeVip("vip-l2", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			Expect(vips.Create(context.Background(), makeVip("vip-bgp", "10.0.0.2", starling_announce.StarlingAnnounce_BGP))).To(Succeed())

			Expect(placer.RecalculateAndApply(context.Background())).To(Succeed())

			Expect(listener.Added()).To(ConsistOf("10.0.0.1"))
			Expect(listener.Removed()).To(BeEmpty())
			Expect(listener.NbAfterCalculate()).To(Equal(1))
			Expect(vipsIps(placer.AnnouncedVips())).To(ConsistOf("10.0.0.1"))
		})

		It("should only notify differences on recalculation", func() {
			Expect(vips.Create(context.Background(), makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			Expect(vips.Create(context.Background(), makeVip("vip-2", "10.0.0.2", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			Expect(placer.RecalculateAndApply(context.Background())).To(Succeed())
			Expect(listener.Added()).To(ConsistOf("10.0.0.1", "10.0.0.2"))

			Expect(vips.Delete(context.Background(), "vip-1")).To(Succeed())
			Expect(vips.Create(context.Background(), makeVip("vip-3", "10.0.0.3", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			Expect(placer.RecalculateAndApply(context.Background())).To(Succeed())

			Expect(listener.Added()).To(ConsistOf("10.0.0.1", "10.0.0.2", "10.0.0.3"))
			Expect(listener.Removed()).To(ConsistOf("10.0.0.1"))
			Expect(vipsIps(placer.AnnouncedVips())).To(ConsistOf("10.0.0.2", "10.0.0.3"))
		})

		It("should aggregate listener errors", func() {
			listener.errOnAdd = fmt.Errorf("cannot add")
			Expect(vips.Create(context.Background(), makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			Expect(vips.Create(context.Background(), makeVip("vip-2", "10.0.0.2", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())

			err := placer.RecalculateAndApply(context.Background())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to add vip 10.0.0.1/32"))
			Expect(err.Error()).To(ContainSubstring("failed to add vip 10.0.0.2/32"))
		})

		It("should not notify a removed listener", func() {
			placer.RemovePlacerListener(listener)
			Expect(vips.Create(context.Background(), makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			Expect(placer.RecalculateAndApply(context.Background())).To(Succeed())
			Expect(listener.Added()).To(BeEmpty())
		})
	})

	When("running in a cluster of agents", func() {
		var (
			stopList2  func() error
			minList1   *AgentList
			minList2   *AgentList
			placer1    *VipPlacer
			placer2    *VipPlacer
			listener1  *recordingPlacerListener
			listener2  *recordingPlacerListener
			nbVips     = 20
			allVipsIps []string
		)

		BeforeEach(func() {
			port1, port2 := freePort(), freePort()
			minList1, _ = startAgentList("node-1", port1, port2)
			minList2, stopList2 = startAgentList("node-2", port2, port1)

			Eventually(func() int { return len(minList1.Members()) }, 10*time.Second).Should(Equal(2))
			Eventually(func() int { return len(minList2.Members()) }, 10*time.Second).Should(Equal(2))

			allVipsIps = make([]string, 0, nbVips)
			for i := range nbVips {
				ip := fmt.Sprintf("10.0.0.%d", i+1)
				allVipsIps = append(allVipsIps, ip)
				Expect(vips.Create(context.Background(), makeVip(fmt.Sprintf("vip-%d", i), ip, starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			}

			listener1 = &recordingPlacerListener{}
			listener2 = &recordingPlacerListener{}
			placer1 = NewVipPlacer(context.Background(), "node-1", minList1, nil, vips)
			placer2 = NewVipPlacer(context.Background(), "node-2", minList2, nil, vips)
			for placer, listener := range map[*VipPlacer]*recordingPlacerListener{placer1: listener1, placer2: listener2} {
				placer.AddPlaceOn(starling_announce.StarlingAnnounce_LAYER2)
				placer.AddPlacerListener(listener)
			}
		})

		AfterEach(func() {
			Expect(placer1.Stop()).To(Succeed())
			Expect(placer2.Stop()).To(Succeed())
		})

		It("should share vips between agents without overlap", func() {
			Expect(placer1.RecalculateAndApply(context.Background())).To(Succeed())
			Expect(placer2.RecalculateAndApply(context.Background())).To(Succeed())

			vips1 := vipsIps(placer1.AnnouncedVips())
			vips2 := vipsIps(placer2.AnnouncedVips())
			Expect(vips1).ToNot(BeEmpty())
			Expect(vips2).ToNot(BeEmpty())
			for _, ip := range vips1 {
				Expect(vips2).ToNot(ContainElement(ip))
			}
			Expect(append(vips1, vips2...)).To(ConsistOf(allVipsIps))
		})

		It("should remove no vip from an agent when it leaves the cluster itself", func() {
			// the uptime strategies place among the members, which a leaving agent is no longer one of
			shortestUptime, _ := GetPlacerStrat("shortest_uptime")
			placer := NewVipPlacer(context.Background(), "node-2", minList2, shortestUptime, vips)
			DeferCleanup(placer.Stop)
			placer.AddPlaceOn(starling_announce.StarlingAnnounce_LAYER2)
			listener := &recordingPlacerListener{}
			placer.AddPlacerListener(listener)
			Expect(placer.RecalculateAndApply(context.Background())).To(Succeed())
			Expect(vipsIps(placer.AnnouncedVips())).To(ConsistOf(allVipsIps))

			Expect(stopList2()).To(Succeed())

			// the vips stay announced while the agent stops, its announcers withdraw them
			Consistently(listener.Removed, time.Second).Should(BeEmpty())
		})

		It("should remove no vip on a recalculation once its agent left the cluster", func() {
			shortestUptime, _ := GetPlacerStrat("shortest_uptime")
			placer := NewVipPlacer(context.Background(), "node-2", minList2, shortestUptime, vips)
			DeferCleanup(placer.Stop)
			placer.AddPlaceOn(starling_announce.StarlingAnnounce_LAYER2)
			listener := &recordingPlacerListener{}
			placer.AddPlacerListener(listener)
			Expect(placer.RecalculateAndApply(context.Background())).To(Succeed())
			Expect(vipsIps(placer.AnnouncedVips())).To(ConsistOf(allVipsIps))
			Expect(stopList2()).To(Succeed())

			// a vip message handled during the stop recalculates
			Expect(placer.RecalculateAndApply(context.Background())).To(Succeed())
			Expect(listener.Removed()).To(BeEmpty())
			Expect(vipsIps(placer.AnnouncedVips())).To(ConsistOf(allVipsIps))
		})

		It("should take over all vips when the other agent leaves", func() {
			Expect(placer1.RecalculateAndApply(context.Background())).To(Succeed())
			Expect(len(placer1.AnnouncedVips())).To(BeNumerically("<", nbVips))

			Expect(stopList2()).To(Succeed())

			// leave event triggers recalculation through node event listener
			Eventually(func() []string { return vipsIps(placer1.AnnouncedVips()) }, 30*time.Second).Should(ConsistOf(allVipsIps))
			Expect(listener1.Added()).To(ConsistOf(allVipsIps))
		})
	})
})
