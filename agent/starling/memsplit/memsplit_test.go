package memsplit_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/agent/starling/memsplit"
	"github.com/seagalia/flock/agent/starling/procmemory"
)

var _ = Describe("New", func() {
	// dev mode never splits, so the shares are of the memory of the test process
	DescribeTable("shares the memory between the agent, the WAF and envoy",
		func(wafConfig *config.WafConfig, agentPercent, wafPercent, envoyPercent uint64) {
			split := memsplit.New(wafConfig, true)
			available := procmemory.Available()
			Expect(split.HasCgroups()).To(BeFalse())
			Expect(split.Agent).To(Equal(available * agentPercent / 100))
			Expect(split.Waf).To(Equal(available * wafPercent / 100))
			Expect(split.Envoy).To(Equal(available * envoyPercent / 100))
			Expect(split.EnvoyPercent).To(Equal(envoyPercent))
		},
		Entry("embedded WAF, inside the agent", &config.WafConfig{WafEmbedded: true}, uint64(40), uint64(20), uint64(60)),
		Entry("exec WAF, beside the agent", &config.WafConfig{WafExec: &config.WafExec{}}, uint64(20), uint64(20), uint64(60)),
		Entry("WAF elsewhere", &config.WafConfig{WafExternal: &config.WafExternal{}}, uint64(20), uint64(0), uint64(80)),
	)
})
