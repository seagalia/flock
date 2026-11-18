//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("HTTP rate limit", func() {
	const (
		vipIP = "10.250.0.190"
		url   = "http://" + vipIP + "/hostname"
	)

	BeforeEach(func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-limited", []string{httpbin1IP + ":8080"}),
			res.Apply(res.HttpVs("vs-limited", 80, res.HttpRoute(res.VirtualHost([]string{"*"}), "c-limited")),
				res.WithRequestRateLimitByHeader("x-user", 2)),
			res.Vip("vip-limited", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-limited"),
		)
	})

	It("limits the requests of each header value on its own", func(ctx SpecContext) {
		statusAs := func(user string) int {
			status, _, err := client.HTTPGet(ctx, url, env.WithHeader("x-user: "+user))
			Expect(err).ToNot(HaveOccurred())
			return status
		}
		// a request without the header builds no descriptor, so waiting for the listener spends no token
		Eventually(func() (int, error) {
			status, _, err := client.HTTPGet(ctx, url)
			return status, err
		}).Should(Equal(200))

		Expect([]int{statusAs("alice"), statusAs("alice"), statusAs("alice")}).To(Equal([]int{200, 200, 429}))
		Expect(statusAs("bob")).To(Equal(200))
	})
})
