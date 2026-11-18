//go:build e2e

package e2e_test

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"

	"github.com/seagalia/flock/e2e/agent/starling/env"
)

const (
	mainInstance = "main"
	mainIP       = "172.31.250.100"
	clientIP     = "172.31.250.50"
	httpbin1IP   = "172.31.250.11"
	httpbin2IP   = "172.31.250.12"
	ppBackendIP  = "172.31.250.13"
	udpEcho1IP   = "172.31.250.14"
	udpEcho2IP   = "172.31.250.15"
	routedVips   = "10.250.0.0/24"
)

var (
	mainBootRules []string
	e2eEnv        *env.Env
	httpbin1      testcontainers.Container
	main          *env.Agent
	client        *env.Client
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(30 * time.Second)
	SetDefaultEventuallyPollingInterval(500 * time.Millisecond)
	RunSpecs(t, "flock-agent e2e Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	var err error
	e2eEnv, err = env.Setup(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func(ctx SpecContext) {
		e2eEnv.Teardown(ctx)
	}, NodeTimeout(2*time.Minute))

	httpbin1, err = e2eEnv.StartHttpbin(ctx, "httpbin-1", httpbin1IP)
	Expect(err).ToNot(HaveOccurred())
	_, err = e2eEnv.StartHttpbin(ctx, "httpbin-2", httpbin2IP)
	Expect(err).ToNot(HaveOccurred())

	_, err = e2eEnv.StartProxyProtocolBackend(ctx, ppBackendIP, []string{httpbin1IP + ":8080", httpbin2IP + ":8080"})
	Expect(err).ToNot(HaveOccurred())

	_, err = e2eEnv.StartUdpEcho(ctx, "udp-echo-1", udpEcho1IP)
	Expect(err).ToNot(HaveOccurred())
	_, err = e2eEnv.StartUdpEcho(ctx, "udp-echo-2", udpEcho2IP)
	Expect(err).ToNot(HaveOccurred())

	client, err = e2eEnv.StartClient(ctx, clientIP)
	Expect(err).ToNot(HaveOccurred())

	main, err = e2eEnv.StartAgent(ctx, env.AgentOptions{Name: "agent-main", IP: mainIP, Instance: mainInstance})
	Expect(err).ToNot(HaveOccurred())
	Expect(client.AddRoute(ctx, routedVips, mainIP)).To(Succeed())

	// without config, envoy is live only once its initial xds fetches time out, scenarios must not pay that
	Eventually(func() (string, error) {
		return main.Exec(ctx, "curl", "-s", "http://127.0.0.1:19000/ready")
	}, 2*time.Minute).Should(ContainSubstring("LIVE"))

	// rules the agent creates at startup, they stay for its whole life
	Eventually(func() []string { return main.FlockNftables(ctx) }).Should(ContainElement(ContainSubstring("snat ip to " + mainIP)))
	mainBootRules = main.FlockNftables(ctx)
}, NodeTimeout(15*time.Minute))

// every scenario removes what it pushed, the agent must then be back to its boot state
var _ = AfterEach(func(ctx SpecContext) {
	// a scenario failure was dumped before cleanup, this one catches leftovers found by the clean check
	DeferCleanup(dumpOnFailure)
	e2eEnv.Controller.RemoveAll(mainInstance)
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	Expect(e2eEnv.Controller.WaitAcked(waitCtx, mainInstance)).To(Succeed())
	AssertAgentClean(ctx, main, mainBootRules)
}, NodeTimeout(2*time.Minute))

func AssertAgentClean(ctx context.Context, m *env.Agent, bootRules []string) {
	GinkgoHelper()
	Eventually(func() []env.IpvsService { return m.Ipvs(ctx) }).Should(BeEmpty(), "ipvs services left")
	Eventually(func() []string { return m.EnvoyListeners(ctx) }).Should(BeEmpty(), "envoy listeners left")
	Eventually(func() []string { return m.EnvoyClusters(ctx) }).Should(ConsistOf("xds_agent", "ext_proc_cluster"), "envoy clusters left")
	// secrets are not checked: envoy keeps a removed secret while a draining listener (drain time 600s) uses it
	Eventually(func() []string {
		leftRules := make([]string, 0)
		for _, rule := range m.FlockNftables(ctx) {
			if !slices.Contains(bootRules, rule) {
				leftRules = append(leftRules, rule)
			}
		}
		return leftRules
	}).Should(BeEmpty(), "nftables rules left")
	Eventually(func() []string { return m.LocalRoutes(ctx) }).ShouldNot(ContainElement(Or(
		HavePrefix("10.250.0."), HavePrefix("172.31.250.2"),
	)), "vip local routes left")
}

var dumpedSpec string

var _ = JustAfterEach(dumpOnFailure)

func dumpOnFailure() {
	spec := CurrentSpecReport()
	alreadyDumped := dumpedSpec == spec.FullText()
	if !spec.Failed() || main == nil || alreadyDumped {
		return
	}
	dumpedSpec = spec.FullText()
	dumpCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for name, content := range main.Dump(dumpCtx) {
		AddReportEntry(name, content, types.ReportEntryVisibilityFailureOrVerbose)
	}
	// keeps containers alive to inspect them with docker exec before cleanup
	if pause := os.Getenv("E2E_PAUSE_ON_FAILURE"); pause != "" {
		duration, err := time.ParseDuration(pause)
		if err != nil {
			duration = 10 * time.Minute
		}
		GinkgoWriter.Printf("E2E_PAUSE_ON_FAILURE: pausing %s, agent container %s\n", duration, main.Container.GetContainerID())
		time.Sleep(duration)
	}
}
