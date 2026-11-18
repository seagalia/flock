package backendctl

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("check delays", func() {
	spread := func(delay func() time.Duration) map[time.Duration]bool {
		seen := make(map[time.Duration]bool)
		for range 100 {
			seen[delay()] = true
		}
		return seen
	}

	It("spreads the first checks over the interval, a second at most", func() {
		for delay := range spread(func() time.Duration { return firstCheckDelay(5 * time.Second) }) {
			Expect(delay).To(And(BeNumerically(">=", 0), BeNumerically("<", time.Second)))
		}
		for delay := range spread(func() time.Duration { return firstCheckDelay(100 * time.Millisecond) }) {
			Expect(delay).To(And(BeNumerically(">=", 0), BeNumerically("<", 100*time.Millisecond)))
		}
		Expect(len(spread(func() time.Duration { return firstCheckDelay(5 * time.Second) }))).To(BeNumerically(">", 1))
	})

	It("adds up to a tenth of the interval to the next check", func() {
		delays := spread(func() time.Duration { return nextCheckDelay(5 * time.Second) })
		for delay := range delays {
			Expect(delay).To(And(BeNumerically(">=", 5*time.Second), BeNumerically("<", 5500*time.Millisecond)))
		}
		Expect(len(delays)).To(BeNumerically(">", 1))
	})

	It("keeps a tiny interval as is", func() {
		Expect(nextCheckDelay(5 * time.Nanosecond)).To(Equal(5 * time.Nanosecond))
		Expect(firstCheckDelay(0)).To(Equal(time.Duration(0)))
	})
})
