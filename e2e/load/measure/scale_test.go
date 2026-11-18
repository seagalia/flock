package measure_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/e2e/load/measure"
)

var _ = Describe("Knob", func() {
	It("gives the default without any variable", func() {
		Expect(measure.Knob("CTL_VS", 2000)).To(Equal(2000))
	})

	It("scales the default with LOAD_SCALE", func() {
		GinkgoT().Setenv("LOAD_SCALE", "0.5")
		Expect(measure.Knob("CTL_VS", 2000)).To(Equal(1000))
	})

	It("never scales below 1", func() {
		GinkgoT().Setenv("LOAD_SCALE", "0.0001")
		Expect(measure.Knob("CTL_VS", 2000)).To(Equal(1))
	})

	It("takes its own variable over the scale", func() {
		GinkgoT().Setenv("LOAD_SCALE", "10")
		GinkgoT().Setenv("LOAD_CTL_VS", "7")
		Expect(measure.Knob("CTL_VS", 2000)).To(Equal(7))
	})

	It("refuses an invalid LOAD_SCALE", func() {
		GinkgoT().Setenv("LOAD_SCALE", "abc")
		_, err := measure.Knob("CTL_VS", 2000)
		Expect(err).To(MatchError(ContainSubstring("LOAD_SCALE")))
	})

	It("refuses a non positive knob", func() {
		GinkgoT().Setenv("LOAD_CTL_VS", "-3")
		_, err := measure.Knob("CTL_VS", 2000)
		Expect(err).To(MatchError(ContainSubstring("LOAD_CTL_VS")))
	})
})

var _ = Describe("Duration", func() {
	It("gives the default without any variable, never scaled", func() {
		GinkgoT().Setenv("LOAD_SCALE", "2")
		Expect(measure.Duration("CHURN", 5*time.Minute)).To(Equal(5 * time.Minute))
	})

	It("takes its own variable", func() {
		GinkgoT().Setenv("LOAD_CHURN", "30s")
		Expect(measure.Duration("CHURN", 5*time.Minute)).To(Equal(30 * time.Second))
	})

	It("refuses an invalid duration", func() {
		GinkgoT().Setenv("LOAD_CHURN", "x")
		_, err := measure.Duration("CHURN", 5*time.Minute)
		Expect(err).To(MatchError(ContainSubstring("LOAD_CHURN")))
	})
})

var _ = Describe("Scale", func() {
	It("is 1 by default", func() {
		Expect(measure.Scale()).To(Equal("1"))
	})

	It("is the raw LOAD_SCALE", func() {
		GinkgoT().Setenv("LOAD_SCALE", "0.25")
		Expect(measure.Scale()).To(Equal("0.25"))
	})
})
