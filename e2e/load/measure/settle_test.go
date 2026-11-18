package measure_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/e2e/load/measure"
)

// reader gives the heaps in order, then keeps giving the last one
func reader(heaps ...uint64) func(context.Context) (measure.Sample, error) {
	i := 0
	return func(context.Context) (measure.Sample, error) {
		heap := heaps[min(i, len(heaps)-1)]
		i++
		return measure.Sample{HeapInuse: heap}, nil
	}
}

var _ = Describe("Settle", func() {
	It("gives the sample once three reads stay within 10%", func(ctx SpecContext) {
		sample, err := measure.Settle(ctx, reader(100, 200, 300, 301, 302, 400), time.Millisecond, time.Minute)
		Expect(err).ToNot(HaveOccurred())
		Expect(sample.HeapInuse).To(Equal(uint64(302)))
		Expect(sample.Unsettled).To(BeFalse())
	})

	It("tolerates the sawtooth of the garbage collector", func(ctx SpecContext) {
		reads := 0
		sawtooth := func(context.Context) (measure.Sample, error) {
			reads++
			return measure.Sample{HeapInuse: uint64(100 + 8*(reads%2))}, nil
		}
		sample, err := measure.Settle(ctx, sawtooth, time.Millisecond, 50*time.Millisecond)
		Expect(err).ToNot(HaveOccurred())
		Expect(sample).To(HaveField("Unsettled", false))
	})

	It("marks the sample unsettled after the max wait", func(ctx SpecContext) {
		heap := uint64(100)
		growing := func(context.Context) (measure.Sample, error) {
			heap *= 2
			return measure.Sample{HeapInuse: heap}, nil
		}
		sample, err := measure.Settle(ctx, growing, 5*time.Millisecond, 50*time.Millisecond)
		Expect(err).ToNot(HaveOccurred())
		Expect(sample.Unsettled).To(BeTrue())
		Expect(sample.HeapInuse).To(BeNumerically(">", 100))
	})

	It("gives the read error", func(ctx SpecContext) {
		failing := func(context.Context) (measure.Sample, error) { return measure.Sample{}, errors.New("scrape failed") }
		_, err := measure.Settle(ctx, failing, time.Millisecond, time.Minute)
		Expect(err).To(MatchError("scrape failed"))
	})
})
