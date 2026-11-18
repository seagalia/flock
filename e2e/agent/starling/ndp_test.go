//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/helpers"

	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("NDP", func() {
	// vip in the ipv6 subnet of the network, outside the addresses docker gives: the client resolves it with ndp
	var vipIP string

	// ping makes the client solicit the vip, then gives the mac it learnt
	solicitedMAC := func(ctx SpecContext) func() (string, error) {
		return func() (string, error) {
			_, err := client.Exec(ctx, "ping", "-6", "-c", "1", "-W", "2", vipIP)
			if err != nil {
				return "", err
			}
			return client.Neigh(ctx, vipIP), nil
		}
	}

	BeforeEach(func(ctx SpecContext) {
		vipIP = e2eEnv.V6Prefix + "1:200"
		v6Vip := res.Vip("vip-ndp", vipIP, starling_announce.StarlingAnnounce_LAYER2, "vs-ndp")
		v6Vip.PrefixLength = 128
		// the backends are ipv4: envoy serves the vip, ipvs cannot change the address family
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-ndp", []string{httpbin1IP + ":8080"}),
			res.HttpVs("vs-ndp", 80, res.HttpRoute(res.VirtualHost([]string{"*"}), "c-ndp")),
			v6Vip,
		)
	})

	It("answers neighbor solicitations for the vip and serves it", func(ctx SpecContext) {
		Eventually(solicitedMAC(ctx)).Should(Equal(main.MAC(ctx)))
		Eventually(func() (string, error) {
			return client.Hostname(ctx, "http://["+vipIP+"]/hostname")
		}).Should(Equal("httpbin-1"))
	})

	It("stops answering once the vip is removed", func(ctx SpecContext) {
		Eventually(solicitedMAC(ctx)).Should(Equal(main.MAC(ctx)))
		e2eEnv.Controller.Remove(mainInstance, helpers.TypeUrl[*vip.VIP](), "vip-ndp")
		Eventually(func() (string, error) {
			return main.Exec(ctx, "ip", "-6", "-o", "addr", "show", "dev", "flock0")
		}).ShouldNot(ContainSubstring(vipIP))
		Eventually(func() error {
			_, err := client.Exec(ctx, "ip", "-6", "neigh", "flush", "dev", "eth0")
			if err != nil {
				return err
			}
			_, err = solicitedMAC(ctx)()
			return err
		}).Should(HaveOccurred())
	})
})
