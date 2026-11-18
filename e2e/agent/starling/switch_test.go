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

var _ = Describe("IPVS to Envoy switch under traffic", func() {
	const (
		vipIP        = "10.250.0.80"
		maxRecovery  = time.Minute
		requestEvery = 100 * time.Millisecond
	)
	vipRule := ContainSubstring(vipIP + " . 80 : jump")

	servedByIpvs := func(ctx SpecContext) {
		GinkgoHelper()
		Eventually(func() []env.IpvsService { return main.Ipvs(ctx) }, maxRecovery).Should(HaveLen(1))
		Eventually(func() []string { return main.EnvoyListeners(ctx) }, maxRecovery).ShouldNot(ContainElement("tcp-80/vip-switch"))
	}
	servedByEnvoy := func(ctx SpecContext) {
		GinkgoHelper()
		Eventually(func() []string { return main.EnvoyListeners(ctx) }, maxRecovery).Should(ContainElement("tcp-80/vip-switch"))
		Eventually(func() []env.IpvsService { return main.Ipvs(ctx) }, maxRecovery).Should(BeEmpty())
		Eventually(func() []string { return main.FlockNftables(ctx) }, maxRecovery).ShouldNot(ContainElement(vipRule), "no snat left from ipvs")
	}

	// switchTo measures recovery from the push, but only once the node state shows the new path
	switchTo := func(ctx SpecContext, loop *env.Loop, backendUuid, direction string, servedByNewPath func(SpecContext)) {
		GinkgoHelper()
		switchedAt := time.Now()
		e2eEnv.Controller.Push(mainInstance, res.TcpVs("vs-switch", 80, backendUuid))
		servedByNewPath(ctx)
		pathSwitchedAt := time.Now()

		Eventually(func() bool {
			isRecovered := loop.RecoveryAfter(switchedAt).Recovered
			isNewPathClean := loop.RecoveryAfter(pathSwitchedAt).Failed == 0
			isStable := time.Since(pathSwitchedAt) > time.Second
			return isRecovered && isNewPathClean && isStable
		}, maxRecovery+5*time.Second).Should(BeTrue(), "traffic must recover within %s", maxRecovery)

		recovery := loop.RecoveryAfter(switchedAt)
		Expect(recovery.BrokenFor).To(BeNumerically("<=", maxRecovery))
		report := fmt.Sprintf("recovered after %s, %d failed requests, new path ready after %s",
			recovery.BrokenFor, recovery.Failed, pathSwitchedAt.Sub(switchedAt))
		AddReportEntry(direction, report)
		GinkgoWriter.Printf("%s: %s\n", direction, report)
	}

	It("keeps traffic working when switching from ipvs to envoy and back", func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-plain", []string{httpbin1IP + ":8080", httpbin2IP + ":8080"}),
			res.Backend("c-proxied", []string{ppBackendIP + ":8081"}, res.WithProxyProtocolV2()),
			res.TcpVs("vs-switch", 80, "c-plain"),
			res.TcpVs("vs-warm", 81, "c-proxied"),
			res.Vip("vip-switch", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-switch", "vs-warm"),
		)
		servedByIpvs(ctx)
		Eventually(func() (string, error) { return client.Hostname(ctx, "http://"+vipIP+"/hostname") }).ShouldNot(BeEmpty())
		// a backend is checked only once a virtual server uses it, and its first check waits up to a second: switched
		// to a backend nothing used, envoy serves the vip with no endpoint for a moment. vs-warm keeps c-proxied checked,
		// so envoy holds its endpoint before the switch.
		Eventually(func() []string { return main.EnvoyClusterHosts(ctx, "c-proxied") }, maxRecovery).
			Should(ContainElement(ppBackendIP + ":8081"))

		loop := client.StartLoop(ctx, "http://"+vipIP+"/hostname", requestEvery)
		DeferCleanup(loop.Stop)

		switchTo(ctx, loop, "c-proxied", "ipvs -> envoy", servedByEnvoy)
		switchTo(ctx, loop, "c-plain", "envoy -> ipvs", servedByIpvs)

		recoveredAt := time.Now()
		Consistently(func() int {
			return loop.RecoveryAfter(recoveredAt).Failed
		}, 10*time.Second).Should(BeZero(), "no failure once recovered")
	})
})
