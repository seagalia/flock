//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("VIPs on flock0", func() {
	const (
		singleVipIP = "10.250.0.140"
		prefixVipIP = "10.250.0.130"
		prefixCidr  = "10.250.0.128/28"
	)

	BeforeEach(func(ctx SpecContext) {
		prefixVip := res.Vip("prefix-vip", prefixVipIP, starling_announce.StarlingAnnounce_STATIC, "vs-prefix")
		prefixVip.PrefixLength = 28
		// one virtual server per vip: the agent may miss a vip attached at the same time as its virtual server
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-flock0", []string{httpbin1IP + ":8080"}),
			res.HttpVs("vs-single", 80, res.HttpRoute(res.VirtualHost([]string{"*"}), "c-flock0")),
			res.HttpVs("vs-prefix", 80, res.HttpRoute(res.VirtualHost([]string{"*"}), "c-flock0")),
			res.Vip("single-vip", singleVipIP, starling_announce.StarlingAnnounce_STATIC, "vs-single"),
			prefixVip,
		)
	})

	It("gives a single ip vip as a host scoped address", func(ctx SpecContext) {
		Eventually(func() (string, error) {
			return main.Exec(ctx, "ip", "-4", "-o", "addr", "show", "dev", "flock0")
		}).Should(ContainSubstring("inet " + singleVipIP + "/32 scope host"))
		Eventually(func() (string, error) {
			return client.Hostname(ctx, "http://"+singleVipIP+"/hostname")
		}).Should(Equal("httpbin-1"))
	})

	It("makes every ip of a prefix vip local with an AnyIP route", func(ctx SpecContext) {
		Eventually(func() []string { return main.LocalRoutes(ctx) }).Should(ContainElement(prefixCidr))
		addrs, err := main.Exec(ctx, "ip", "-4", "-o", "addr", "show", "dev", "flock0")
		Expect(err).ToNot(HaveOccurred())
		Expect(addrs).ToNot(ContainSubstring("10.250.0.128"), "no address, it would only make the network ip local")
		Eventually(func() (string, error) {
			return client.Hostname(ctx, "http://"+prefixVipIP+"/hostname")
		}).Should(Equal("httpbin-1"))
	})
})
