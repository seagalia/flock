//go:build e2e

package e2e_test

import (
	"regexp"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var l3logMetric = regexp.MustCompile(`flock_agent_l3logs_incoming_packets_total\{[^}]*vs_uuid="vs-lim"[^}]*\} ([0-9.e+]+)`)

func l3logPackets(ctx SpecContext) float64 {
	total := 0.0
	for _, match := range l3logMetric.FindAllStringSubmatch(main.Metrics(ctx), -1) {
		value, err := strconv.ParseFloat(match[1], 64)
		if err == nil {
			total += value
		}
	}
	return total
}

var _ = Describe("Limits and L3 logs", func() {
	const (
		vipIP = "10.250.0.70"
		url   = "http://" + vipIP + "/hostname"
	)
	vsInputChain := ContainSubstring("vs_10_250_0_70_32_80_tcp_in")

	BeforeEach(func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-lim", []string{httpbin1IP + ":8080", httpbin2IP + ":8080"}),
			res.Vip("vip-lim", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-lim"),
		)
	})

	It("creates limit rules and removes them when limits are dropped", func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance, res.TcpVs("vs-lim", 80, "c-lim", res.WithConnLimit(10), res.WithRateLimit(100, 20)))
		Eventually(func() []string { return main.FlockNftables(ctx) }).Should(And(
			ContainElement(ContainSubstring(vipIP+" . 80 : jump")),
			ContainElement(vsInputChain),
			ContainElement(And(ContainSubstring("ct count over 10"), ContainSubstring("drop"))),
			ContainElement(ContainSubstring("limit rate over 100/second burst 20 packets")),
		))

		e2eEnv.Controller.Push(mainInstance, res.TcpVs("vs-lim", 80, "c-lim"))
		Eventually(func() []string { return main.FlockNftables(ctx) }).ShouldNot(Or(
			ContainElement(ContainSubstring("ct count")),
			ContainElement(ContainSubstring("limit rate")),
		))
		Eventually(func() (string, error) { return client.Hostname(ctx, url) }).ShouldNot(BeEmpty())
	})

	It("drops over the limit even when another table accepts first", func(ctx SpecContext) {
		// an accept only ends its own base chain: the agent drop must still apply after it
		for _, cmd := range [][]string{
			{"nft", "add", "table", "ip", "foreign"},
			{"nft", "add", "chain", "ip", "foreign", "input", "{ type filter hook input priority -100 ; }"},
			{"nft", "add", "rule", "ip", "foreign", "input", "tcp", "dport", "80", "accept"},
		} {
			_, err := main.Exec(ctx, cmd...)
			Expect(err).ToNot(HaveOccurred())
		}
		DeferCleanup(func(ctx SpecContext) { _, _ = main.Exec(ctx, "nft", "delete", "table", "ip", "foreign") })

		e2eEnv.Controller.Push(mainInstance, res.TcpVs("vs-lim", 80, "c-lim", res.WithSourceConnLimit(1)))
		Eventually(func() []string { return main.FlockNftables(ctx) }).Should(ContainElement(ContainSubstring("ct count over 1")))
		Eventually(func() (string, error) { return client.Hostname(ctx, url) }).ShouldNot(BeEmpty())

		_, err := client.Exec(ctx, "sh", "-c",
			"rm -f /tmp/held.out; curl -s --max-time 20 http://"+vipIP+"/delay/10 > /tmp/held.out 2>&1 &")
		Expect(err).ToNot(HaveOccurred())
		// retried until the held connection is established
		Eventually(func() error {
			_, err := client.Hostname(ctx, url, env.WithMaxTime(2*time.Second))
			return err
		}, 8*time.Second).Should(HaveOccurred(), "a second connection from the same source is over the limit")

		Eventually(func() (string, error) { return client.Exec(ctx, "cat", "/tmp/held.out") }, 20*time.Second).
			Should(ContainSubstring(`"url"`), "the held connection is served")
		Eventually(func() (string, error) { return client.Hostname(ctx, url) }, 10*time.Second).ShouldNot(BeEmpty())
	})

	It("logs packets with l3 log", func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance, res.TcpVs("vs-lim", 80, "c-lim", res.WithL3Log()))
		Eventually(func() []string { return main.FlockNftables(ctx) }).Should(
			ContainElement(ContainSubstring(`log prefix "vs-lim" group 15182`)),
		)

		Eventually(func() float64 {
			for range 5 {
				_, _ = client.Hostname(ctx, url)
			}
			return l3logPackets(ctx)
		}).Should(BeNumerically(">", 0))
	})
})
