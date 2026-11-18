package cli

import (
	"github.com/logrusorgru/aurora/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AutoColor", func() {
	It("gives the same color to the same id", func() {
		autoColor := NewAutoColor()

		first := autoColor.GetColor("agent-1")

		Expect(autoColor.GetColor("agent-1")).To(Equal(first))
	})

	It("gives distinct colors to new ids, starting with red", func() {
		autoColor := NewAutoColor()

		Expect(autoColor.GetColor("agent-1")).To(Equal(aurora.RedFg))
		Expect(autoColor.GetColor("agent-2")).To(Equal(aurora.GreenFg))
		Expect(autoColor.GetColor("agent-3")).To(Equal(aurora.YellowFg))
	})

	It("cycles through the seven colors", func() {
		autoColor := NewAutoColor()
		for _, id := range []string{"1", "2", "3", "4", "5", "6"} {
			autoColor.GetColor(id)
		}

		Expect(autoColor.GetColor("7")).To(Equal(aurora.BlackFg))
		Expect(autoColor.GetColor("8")).To(Equal(aurora.RedFg))
	})
})
