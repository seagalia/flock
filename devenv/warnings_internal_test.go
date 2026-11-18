package devenv

import (
	"bytes"
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("warnNotForProduction", func() {
	var saved time.Duration

	BeforeEach(func() { saved = notForProductionWait })
	AfterEach(func() { notForProductionWait = saved })

	It("should wait before returning", func() {
		notForProductionWait = 50 * time.Millisecond
		var out bytes.Buffer

		start := time.Now()
		Expect(warnNotForProduction(context.Background(), &out)).To(Succeed())

		Expect(time.Since(start)).To(BeNumerically(">=", 50*time.Millisecond))
		Expect(out.String()).To(ContainSubstring("not for production"))
	})

	It("should return at once when stopped", func() {
		notForProductionWait = time.Minute
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		start := time.Now()
		err := warnNotForProduction(ctx, &bytes.Buffer{})

		Expect(err).To(MatchError(context.Canceled))
		Expect(time.Since(start)).To(BeNumerically("<", time.Second))
	})
})
