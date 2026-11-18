package agentlist

import (
	"strings"

	gometrics "github.com/hashicorp/go-metrics"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/seagalia/flock/agent/starling/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var _ = Describe("memberlist metrics", func() {
	// memberlist reports through the hashicorp/go-metrics global, it must reach prometheus
	It("exposes metrics sent through hashicorp/go-metrics once the agent enables its metrics", func() {
		sdkmetrics.EnableAppMetrics(config.AppName)
		gometrics.IncrCounter([]string{"memberlist", "test", "probe"}, 1)

		families, err := prometheus.DefaultGatherer.Gather()
		Expect(err).ToNot(HaveOccurred())
		var names []string
		for _, family := range families {
			if strings.Contains(family.GetName(), "memberlist_test_probe") {
				names = append(names, family.GetName())
			}
		}
		Expect(names).ToNot(BeEmpty())
	})
})
