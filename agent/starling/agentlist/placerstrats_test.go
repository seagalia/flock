package agentlist_test

import (
	"fmt"
	"time"

	"github.com/hashicorp/memberlist"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	. "github.com/seagalia/flock/agent/starling/agentlist"
)

func nodeStartedAgo(name string, ago time.Duration) *memberlist.Node {
	return &memberlist.Node{
		Name: name,
		Meta: []byte(time.Now().Add(-ago).Format(time.RFC3339Nano)),
	}
}

func emptyPlacement(nodeNames ...string) map[string][]*vip.VIP {
	placement := make(map[string][]*vip.VIP)
	for _, name := range nodeNames {
		placement[name] = make([]*vip.VIP, 0)
	}
	return placement
}

var _ = Describe("PlacerStrats", func() {
	Context("GetPlacerStrat", func() {
		It("should give default strategy when name is empty", func() {
			strategy, ok := GetPlacerStrat("")
			Expect(ok).To(BeTrue())
			Expect(strategy.Name()).To(Equal(DefaultPlacerStrat))
		})
		DescribeTable("should find strategy by name case insensitively",
			func(name, expectedName string) {
				strategy, ok := GetPlacerStrat(name)
				Expect(ok).To(BeTrue())
				Expect(strategy.Name()).To(Equal(expectedName))
			},
			Entry("distributed", "distributed", "distributed"),
			Entry("longest uptime", "LONGEST_UPTIME", "longest_uptime"),
			Entry("shortest uptime", "Shortest_Uptime", "shortest_uptime"),
		)
		It("should not find an unknown strategy", func() {
			_, ok := GetPlacerStrat("random")
			Expect(ok).To(BeFalse())
		})
		It("should list all strategies", func() {
			Expect(GetPlacerStrats()).To(ConsistOf("distributed", "longest_uptime", "shortest_uptime"))
		})
	})

	Context("Distributed", func() {
		var strategy PlacerStrat

		BeforeEach(func() {
			strategy = &Distributed{}
		})

		It("should place each vip on exactly one node", func() {
			placement := emptyPlacement("node-1", "node-2", "node-3")
			for i := range 100 {
				strategy.Placing(nil, placement, makeVip(fmt.Sprintf("vip-%d", i), fmt.Sprintf("10.0.%d.%d", i/250, i%250+1), starling_announce.StarlingAnnounce_LAYER2))
			}
			total := 0
			for _, vips := range placement {
				total += len(vips)
			}
			Expect(total).To(Equal(100))
		})

		It("should spread vips across nodes", func() {
			placement := emptyPlacement("node-1", "node-2", "node-3")
			for i := range 300 {
				strategy.Placing(nil, placement, makeVip(fmt.Sprintf("vip-%d", i), fmt.Sprintf("10.0.%d.%d", i/250, i%250+1), starling_announce.StarlingAnnounce_LAYER2))
			}
			for node, vips := range placement {
				Expect(len(vips)).To(BeNumerically(">", 30), "node %s should have a fair share of vips", node)
			}
		})

		It("should be deterministic whatever the map iteration order is", func() {
			aVip := makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2)
			var chosen string
			for range 20 {
				placement := emptyPlacement("node-1", "node-2", "node-3", "node-4")
				strategy.Placing(nil, placement, aVip)
				for node, vips := range placement {
					if len(vips) == 0 {
						continue
					}
					if chosen == "" {
						chosen = node
					}
					Expect(node).To(Equal(chosen))
				}
			}
		})
	})

	Context("LongestUptime", func() {
		It("should place vip on the node with the longest uptime", func() {
			nodes := []*memberlist.Node{
				nodeStartedAgo("young", 1*time.Minute),
				nodeStartedAgo("old", 10*time.Hour),
				nodeStartedAgo("middle", 1*time.Hour),
			}
			placement := emptyPlacement("young", "old", "middle")
			(&LongestUptime{}).Placing(nodes, placement, makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))
			Expect(placement["old"]).To(HaveLen(1))
			Expect(placement["young"]).To(BeEmpty())
			Expect(placement["middle"]).To(BeEmpty())
		})
		It("should fallback to a known node when there is no member", func() {
			placement := emptyPlacement("me")
			(&LongestUptime{}).Placing(nil, placement, makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))
			Expect(placement["me"]).To(HaveLen(1))
		})
	})

	Context("ShortestUptime", func() {
		It("should place vip on the node with the shortest uptime", func() {
			nodes := []*memberlist.Node{
				nodeStartedAgo("old", 10*time.Hour),
				nodeStartedAgo("young", 1*time.Minute),
				nodeStartedAgo("middle", 1*time.Hour),
			}
			placement := emptyPlacement("young", "old", "middle")
			(&ShortestUptime{}).Placing(nodes, placement, makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))
			Expect(placement["young"]).To(HaveLen(1))
			Expect(placement["old"]).To(BeEmpty())
		})
		It("should fallback to a known node when there is no member", func() {
			placement := emptyPlacement("me")
			(&ShortestUptime{}).Placing(nil, placement, makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))
			Expect(placement["me"]).To(HaveLen(1))
		})
	})
})

var _ = Describe("UptimeDelegate", func() {
	It("should expose start time as node meta", func() {
		before := time.Now()
		delegate := NewUptimeDelegate()
		meta := delegate.NodeMeta(512)

		node := &memberlist.Node{Meta: meta}
		startTime := NodeStartTime(node)
		Expect(startTime).To(BeTemporally(">=", before))
		Expect(startTime).To(BeTemporally("<=", time.Now()))
		Expect(NodeUptime(node)).To(BeNumerically(">=", 0))
	})

	It("should truncate node meta to the given limit", func() {
		delegate := NewUptimeDelegate()
		Expect(delegate.NodeMeta(4)).To(HaveLen(4))
	})

	It("should reset start time", func() {
		delegate := NewUptimeDelegate()
		firstStart := NodeStartTime(&memberlist.Node{Meta: delegate.NodeMeta(512)})
		time.Sleep(5 * time.Millisecond)
		delegate.ResetStartTime()
		secondStart := NodeStartTime(&memberlist.Node{Meta: delegate.NodeMeta(512)})
		Expect(secondStart).To(BeTemporally(">", firstStart))
	})

	It("should give zero start time on invalid meta", func() {
		Expect(NodeStartTime(&memberlist.Node{Meta: []byte("not a time")})).To(BeZero())
	})
})
