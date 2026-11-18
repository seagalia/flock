//go:build e2e

package e2e_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("Disabled init rules", Ordered, func() {
	const (
		instance = "noinit"
		agentIP  = "172.31.250.108"
		vipIP    = "10.250.0.180"
	)
	var agent *env.Agent

	BeforeAll(func(ctx SpecContext) {
		var err error
		agent, err = e2eEnv.StartAgent(ctx, env.AgentOptions{
			Name: "agent-noinit", IP: agentIP, Instance: instance,
			Configure: func(conf *config.Config) {
				conf.AdvertisementConfig.NetworkManager = &config.NetworkManagerConfig{DisableInitRules: true}
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(client.AddRoute(ctx, vipIP+"/32", agentIP)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) {
			_, _ = client.Exec(ctx, "ip", "route", "del", vipIP+"/32")
			e2eEnv.Controller.RemoveAll(instance)
			_ = agent.Stop(ctx)
		})
	}, NodeTimeout(5*time.Minute))

	It("keeps the vs rules without the init rules", func(ctx SpecContext) {
		Eventually(func() []string { return agent.FlockNftables(ctx) }, "1m").Should(ContainElement(ContainSubstring("snat ip to " + agentIP)))
		Expect(agent.FlockNftables(ctx)).ToNot(ContainElement(Or(
			ContainSubstring("ct state established"),
			ContainSubstring("ct state invalid"),
		)))

		e2eEnv.Controller.Push(instance,
			res.Backend("c-noinit", []string{httpbin1IP + ":8080", httpbin2IP + ":8080"}),
			res.TcpVs("vs-noinit", 80, "c-noinit", res.WithConnLimit(10)),
			res.Vip("vip-noinit", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-noinit"),
		)
		Eventually(func() []string { return agent.FlockNftables(ctx) }).Should(ContainElement(ContainSubstring("ct count over 10")))
		Eventually(func() (string, error) { return client.Hostname(ctx, "http://"+vipIP+"/hostname") }).ShouldNot(BeEmpty())
	})
})
