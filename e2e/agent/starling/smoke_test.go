//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("Smoke", func() {
	It("connects to controller and starts envoy", func(ctx SpecContext) {
		Eventually(func() bool {
			return e2eEnv.Controller.Subscribed(mainInstance,
				helpers.TypeUrl[*vip.VIP](),
				helpers.TypeUrl[*vs.VirtualServer](),
				helpers.TypeUrl[*backend.Backend](),
				helpers.TypeUrl[*cert.Certificate](),
			)
		}).Should(BeTrue())

		Eventually(func() (string, error) {
			return main.Exec(ctx, "curl", "-s", "http://127.0.0.1:19000/ready")
		}).Should(ContainSubstring("LIVE"))

		Expect(main.FlockNftables(ctx)).To(ContainElement(And(
			ContainSubstring(`oifname "eth0"`),
			ContainSubstring("0x00003b4d"),
			ContainSubstring("snat ip to "+mainIP),
		)))
	})

	It("runs the WAF embedded in the agent", func(ctx SpecContext) {
		Eventually(func() error {
			_, err := main.Exec(ctx, "test", "-S", "/tmp/starling-waf.sock")
			return err
		}).Should(Succeed())
		// embedded, the WAF serves no metrics socket, its series are in the registry of the agent
		Expect(main.Metrics(ctx)).To(ContainSubstring("starling_waf_grpc_server_started_total"))
	})
})
