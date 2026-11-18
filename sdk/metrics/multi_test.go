package metrics_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/seagalia/flock/sdk/metrics"
)

func gathered(name string) bool {
	families, err := prometheus.DefaultGatherer.Gather()
	Expect(err).ToNot(HaveOccurred())
	for _, family := range families {
		if family.GetName() == name {
			return true
		}
	}
	return false
}

var _ = Describe("app metrics", func() {
	It("serves the metrics of an app only once it is enabled", func() {
		counter := metrics.NewCounter("spec-app-before", prometheus.CounterOpts{Name: "spec_before_total"})
		DeferCleanup(func() { prometheus.Unregister(counter) })
		Expect(gathered("spec_before_total")).To(BeFalse())

		metrics.EnableAppMetrics("spec-app-before")

		Expect(gathered("spec_before_total")).To(BeTrue())
	})

	It("registers a metric of an enabled app directly in the default registry", func() {
		early := metrics.NewCounter("spec-app-after", prometheus.CounterOpts{Name: "spec_early_total"})
		DeferCleanup(func() { prometheus.Unregister(early) })
		metrics.EnableAppMetrics("spec-app-after")
		gauge := metrics.NewGauge("spec-app-after", prometheus.GaugeOpts{Name: "spec_after"})
		DeferCleanup(func() { prometheus.Unregister(gauge) })

		Expect(gathered("spec_after")).To(BeTrue())
	})

	It("does nothing when an app is enabled again", func() {
		counter := metrics.NewCounter("spec-app-twice", prometheus.CounterOpts{Name: "spec_twice_total"})
		DeferCleanup(func() { prometheus.Unregister(counter) })
		metrics.EnableAppMetrics("spec-app-twice")

		Expect(func() { metrics.EnableAppMetrics("spec-app-twice") }).ToNot(Panic())
		Expect(gathered("spec_twice_total")).To(BeTrue())
	})

	It("keeps the metrics of an app that is never enabled out of the default registry", func() {
		metrics.EnableAppMetrics("spec-app-other")
		metrics.NewCounter("spec-app-idle", prometheus.CounterOpts{Name: "spec_idle_total"})

		Expect(gathered("spec_idle_total")).To(BeFalse())
	})
})
