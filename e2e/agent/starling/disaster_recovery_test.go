//go:build e2e

package e2e_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/helpers"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("Disaster recovery when controller is unreachable", func() {
	const (
		ipvsVipIP    = "10.250.0.110"
		envoyVipIP   = "10.250.0.111"
		removedVipIP = "10.250.0.112"
		addedVipIP   = "10.250.0.113"
		// sdk backoff: 1s doubled on each failure up to 30s
		maxBackoff = 30 * time.Second
	)
	vipTypeUrl := helpers.TypeUrl[*vip.VIP]()

	ipvsAddrs := func(ctx SpecContext) []string {
		addrs := make([]string, 0)
		for _, service := range main.Ipvs(ctx) {
			addrs = append(addrs, service.Addr)
		}
		return addrs
	}

	It("keeps serving, retries with backoff and applies what changed meanwhile", func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-both", []string{httpbin1IP + ":8080", httpbin2IP + ":8080"}),
			res.Backend("c-app", []string{httpbin1IP + ":8080"}),
			res.TcpVs("vs-ipvs", 80, "c-both"),
			res.Vip("vip-ipvs", ipvsVipIP, starling_announce.StarlingAnnounce_STATIC, "vs-ipvs"),
			res.HttpVs("vs-http", 80, res.HttpRoute(res.VirtualHost([]string{"app.e2e.local"}), "c-app")),
			res.Vip("vip-http", envoyVipIP, starling_announce.StarlingAnnounce_STATIC, "vs-http"),
			res.TcpVs("vs-removed", 80, "c-both"),
			res.Vip("vip-removed", removedVipIP, starling_announce.StarlingAnnounce_STATIC, "vs-removed"),
		)
		appHostname := func() (string, error) {
			return client.Hostname(ctx, "http://"+envoyVipIP+"/hostname", env.WithHeader("Host: app.e2e.local"))
		}
		Eventually(func() []string { return ipvsAddrs(ctx) }).Should(ConsistOf(ipvsVipIP+":80", removedVipIP+":80"))
		Eventually(appHostname).Should(Equal("httpbin-1"))
		Eventually(func() (string, error) { return client.Hostname(ctx, "http://"+ipvsVipIP+"/hostname") }).ShouldNot(BeEmpty())

		ipvsLoop := client.StartLoop(ctx, "http://"+ipvsVipIP+"/hostname", 200*time.Millisecond)
		DeferCleanup(ipvsLoop.Stop)
		envoyLoop := client.StartLoop(ctx, "http://"+envoyVipIP+"/hostname", 200*time.Millisecond, env.WithHeader("Host: app.e2e.local"))
		DeferCleanup(envoyLoop.Stop)

		outageAt := time.Now()
		e2eEnv.Controller.StartOutage(mainInstance)
		DeferCleanup(e2eEnv.Controller.EndOutage, mainInstance)

		e2eEnv.Controller.Remove(mainInstance, helpers.TypeUrl[*vs.VirtualServer](), "vs-removed")
		e2eEnv.Controller.Remove(mainInstance, vipTypeUrl, "vip-removed")
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-app", []string{httpbin2IP + ":8080"}),
			res.TcpVs("vs-added", 80, "c-both"),
			res.Vip("vip-added", addedVipIP, starling_announce.StarlingAnnounce_STATIC, "vs-added"),
		)

		By("retrying until the backoff is capped, then still retrying")
		// 7 attempts give the intervals 2s 4s 8s 16s 30s 30s
		Eventually(func() []time.Time {
			return e2eEnv.Controller.Refusals(mainInstance, vipTypeUrl)
		}, 3*time.Minute, time.Second).Should(HaveLen(7))
		refusals := e2eEnv.Controller.Refusals(mainInstance, vipTypeUrl)
		previous := time.Duration(0)
		for i := 1; i < len(refusals); i++ {
			interval := refusals[i].Sub(refusals[i-1])
			Expect(interval).To(BeNumerically(">=", previous*9/10), "backoff never shrinks during the outage")
			Expect(interval).To(BeNumerically("<=", maxBackoff+5*time.Second), "backoff is capped")
			previous = interval
		}
		Expect(refusals[len(refusals)-1].Sub(refusals[len(refusals)-2])).To(BeNumerically(">=", maxBackoff*8/10),
			"retries go on once the backoff is capped")

		By("having kept the whole config while controller was unreachable")
		Expect(ipvsAddrs(ctx)).To(ConsistOf(ipvsVipIP+":80", removedVipIP+":80"))
		Expect(main.EnvoyListeners(ctx)).To(ContainElement("tcp-80/vip-http"))
		Expect(main.LocalRoutes(ctx)).To(ContainElements(ipvsVipIP+"/32", envoyVipIP+"/32", removedVipIP+"/32"))
		Expect(appHostname()).To(Equal("httpbin-1"), "backend changes are not applied before reconnecting")
		Expect(ipvsLoop.RecoveryAfter(outageAt).Failed).To(BeZero(), "ipvs traffic broken during the outage")
		Expect(envoyLoop.RecoveryAfter(outageAt).Failed).To(BeZero(), "envoy traffic broken during the outage")

		By("applying changes made during the outage once controller is back")
		e2eEnv.Controller.EndOutage(mainInstance)
		Eventually(func() []string { return ipvsAddrs(ctx) }, maxBackoff+30*time.Second).Should(ConsistOf(ipvsVipIP+":80", addedVipIP+":80"))
		Eventually(appHostname).Should(Equal("httpbin-2"))
		Eventually(func() []string { return main.LocalRoutes(ctx) }).ShouldNot(ContainElement(removedVipIP + "/32"))
		Eventually(func() []string { return main.FlockNftables(ctx) }).ShouldNot(ContainElement(ContainSubstring(removedVipIP)))
		Eventually(func() (string, error) { return client.Hostname(ctx, "http://"+addedVipIP+"/hostname") }).ShouldNot(BeEmpty())
		Expect(ipvsLoop.RecoveryAfter(outageAt).Failed).To(BeZero(), "ipvs traffic broken by the resync")
	})
})
