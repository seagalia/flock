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

const (
	olderIP   = "172.31.250.106"
	youngerIP = "172.31.250.107"
)

// startUptimePair starts an older then a younger agent of one instance placing vips with strategy
func startUptimePair(ctx SpecContext, instance, strategy string, vips []string) (older, younger *env.Agent) {
	GinkgoHelper()
	placementConfig := func(ownIP, otherIP string) func(*config.Config) {
		return func(conf *config.Config) {
			conf.AdvertisementConfig.L2Config.UseDynamicPlacement = true
			conf.AdvertisementConfig.DynamicPlacementStrategy = strategy
			conf.AdvertisementConfig.AgentList = &config.AgentListConfig{
				ListenHost: ownIP,
				Members:    []string{otherIP + ":18001"},
				Secret:     "e2e-placement-secret",
			}
		}
	}
	var err error
	older, err = e2eEnv.StartAgent(ctx, env.AgentOptions{Name: "agent-older", IP: olderIP, Instance: instance, Configure: placementConfig(olderIP, youngerIP)})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func(ctx SpecContext) {
		e2eEnv.Controller.RemoveAll(instance)
		_ = older.Container.Terminate(ctx)
	})
	for i, ip := range vips {
		e2eEnv.Controller.Push(instance, res.Vip(strategy+"-"+string(rune('a'+i)), ip, starling_announce.StarlingAnnounce_LAYER2))
	}
	// alone, the older agent announces everything: its uptime is then clearly the longest
	Eventually(func() []string { return older.AnnouncedVips(ctx) }, "1m").Should(HaveLen(len(vips)))

	younger, err = e2eEnv.StartAgent(ctx, env.AgentOptions{Name: "agent-younger", IP: youngerIP, Instance: instance, Configure: placementConfig(youngerIP, olderIP)})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func(ctx SpecContext) {
		_ = younger.Container.Terminate(ctx)
	})
	return older, younger
}

func withMask(ips []string) []string {
	cidrs := make([]string, 0, len(ips))
	for _, ip := range ips {
		cidrs = append(cidrs, ip+"/32")
	}
	return cidrs
}

var _ = Describe("Placement on the longest uptime", Ordered, func() {
	vips := []string{"172.31.250.205", "172.31.250.206"}
	var older, younger *env.Agent

	BeforeAll(func(ctx SpecContext) {
		older, younger = startUptimePair(ctx, "placement-longest", "longest_uptime", vips)
	}, NodeTimeout(5*time.Minute))

	It("keeps every vip on the agent started first", func(ctx SpecContext) {
		// the younger agent must have joined before checking it announces nothing
		Eventually(func() string { return older.Logs(ctx) }, "1m").Should(ContainSubstring("node joined: agent-younger"))
		Consistently(func() []string { return younger.AnnouncedVips(ctx) }, "5s").Should(BeEmpty())
		Expect(older.AnnouncedVips(ctx)).To(ConsistOf(withMask(vips)))
	})

	It("moves every vip to the other agent once SIGUSR2 resets its start time", func(ctx SpecContext) {
		Expect(older.Signal(ctx, "USR2")).To(Succeed())
		Eventually(func() []string { return younger.AnnouncedVips(ctx) }, "1m").Should(ConsistOf(withMask(vips)))
		Eventually(func() []string { return older.AnnouncedVips(ctx) }).Should(BeEmpty())
	})
})

var _ = Describe("Placement on the shortest uptime", Ordered, func() {
	vips := []string{"172.31.250.207", "172.31.250.208"}
	var older, younger *env.Agent

	BeforeAll(func(ctx SpecContext) {
		older, younger = startUptimePair(ctx, "placement-shortest", "shortest_uptime", vips)
	}, NodeTimeout(5*time.Minute))

	It("moves every vip to the agent started last", func(ctx SpecContext) {
		Eventually(func() []string { return younger.AnnouncedVips(ctx) }, "1m").Should(ConsistOf(withMask(vips)))
		Eventually(func() []string { return older.AnnouncedVips(ctx) }).Should(BeEmpty())
	})
})
