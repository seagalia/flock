//go:build e2e

package e2e_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("Lifecycle", Ordered, func() {
	const (
		instance = "lifecycle"
		agentIP  = "172.31.250.103"
		tcpVip   = "10.250.0.90"
		httpVip  = "10.250.0.91"
	)
	var agent *env.Agent

	trafficWorks := func(ctx SpecContext) {
		GinkgoHelper()
		Eventually(func() []string { return distinctHostnames(ctx, "http://"+tcpVip+"/hostname", 10) }).
			Should(ConsistOf("httpbin-1", "httpbin-2"))
		Eventually(func() (string, error) { return client.Hostname(ctx, "http://"+httpVip+"/hostname") }).
			ShouldNot(BeEmpty())
	}

	BeforeAll(func(ctx SpecContext) {
		var err error
		agent, err = e2eEnv.StartAgent(ctx, env.AgentOptions{Name: "agent-lifecycle", IP: agentIP, Instance: instance})
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() (string, error) {
			return agent.Exec(ctx, "curl", "-s", "http://127.0.0.1:19000/ready")
		}, "2m").Should(ContainSubstring("LIVE"))

		// more specific than the route to the main agent
		Expect(client.AddRoute(ctx, tcpVip+"/32", agentIP)).To(Succeed())
		Expect(client.AddRoute(ctx, httpVip+"/32", agentIP)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) {
			_, _ = client.Exec(ctx, "ip", "route", "del", tcpVip+"/32")
			_, _ = client.Exec(ctx, "ip", "route", "del", httpVip+"/32")
			e2eEnv.Controller.RemoveAll(instance)
			_ = agent.Stop(ctx)
		})

		e2eEnv.Controller.Push(instance,
			res.Backend("c-life", []string{httpbin1IP + ":8080", httpbin2IP + ":8080"}),
			res.TcpVs("vs-life-tcp", 80, "c-life"),
			res.Vip("vip-life-tcp", tcpVip, starling_announce.StarlingAnnounce_STATIC, "vs-life-tcp"),
			res.HttpVs("vs-life-http", 80, res.HttpRoute(res.VirtualHost([]string{"*"}), "c-life")),
			res.Vip("vip-life-http", httpVip, starling_announce.StarlingAnnounce_STATIC, "vs-life-http"),
		)
		trafficWorks(ctx)
	}, NodeTimeout(5*time.Minute))

	It("takes over state after a restart without duplicates", func(ctx SpecContext) {
		rulesBefore := agent.FlockNftables(ctx)
		Expect(rulesBefore).ToNot(BeEmpty())
		pidBefore := agent.Pid(ctx)
		Expect(pidBefore).ToNot(BeEmpty())
		loop := client.StartLoop(ctx, "http://"+tcpVip+"/hostname", 100*time.Millisecond)
		DeferCleanup(loop.Stop)

		restartedAt := time.Now()
		Expect(agent.RestartProcess(ctx)).To(Succeed())
		Eventually(func() string { return agent.Pid(ctx) }, "1m").Should(And(Not(BeEmpty()), Not(Equal(pidBefore))))

		Eventually(func() bool {
			isRecovered := loop.RecoveryAfter(restartedAt).Recovered
			isStable := time.Since(restartedAt) > 5*time.Second
			return isRecovered && isStable
		}, "1m").Should(BeTrue())
		recovery := loop.RecoveryAfter(restartedAt)
		Expect(recovery.BrokenFor).To(BeNumerically("<=", time.Minute))
		report := fmt.Sprintf("restart: recovered after %s, %d failed requests", recovery.BrokenFor, recovery.Failed)
		AddReportEntry("restart", report)
		GinkgoWriter.Println(report)

		trafficWorks(ctx)
		services := agent.Ipvs(ctx)
		Expect(services).To(HaveLen(1))
		Expect(services[0].Dests).To(HaveLen(2))

		Expect(agent.FlockNftables(ctx)).To(Equal(rulesBefore), "the restarted agent programs the same table")
	})

	It("hooks its table before the iptables rules", func(ctx SpecContext) {
		Expect(agent.Exec(ctx, "nft", "list", "chain", "inet", "flock", "prerouting")).To(ContainSubstring("priority mangle - 5"))
		Expect(agent.Exec(ctx, "nft", "list", "chain", "inet", "flock", "input")).To(ContainSubstring("priority filter - 10"))
	})

	It("cleans everything it programmed on SIGUSR1", func(ctx SpecContext) {
		Expect(agent.StopRestarting(ctx)).To(Succeed())
		Expect(agent.Signal(ctx, "USR1")).To(Succeed())
		Eventually(func() string { return agent.Pid(ctx) }, "1m").Should(BeEmpty())

		Expect(agent.Ipvs(ctx)).To(BeEmpty())
		Expect(agent.LocalRoutes(ctx)).ToNot(ContainElement(HavePrefix("10.250.0.9")))
		_, err := agent.Exec(ctx, "ip", "link", "show", "flock0")
		Expect(err).To(HaveOccurred(), "clean deletes flock0 with its vips")
		Expect(agent.FlockNftables(ctx)).To(BeEmpty(), "clean deletes the agent table")
		_, err = agent.Exec(ctx, "curl", "-s", "--max-time", "2", "http://127.0.0.1:19000/ready")
		Expect(err).To(HaveOccurred(), "envoy is stopped with the agent")
	})
})
