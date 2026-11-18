package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("numberRange", func() {
	It("reads a lower bound above the upper one as outside the range, as protovalidate does", func() {
		numbers := numberRange{lower: &bound{value: 1000}, upper: &bound{value: 0}}

		Expect(numbers.accepts(0)).To(BeTrue())
		Expect(numbers.accepts(500)).To(BeFalse())
		Expect(numbers.accepts(1000)).To(BeTrue())
		Expect(numbers.constraints(typeBounds{}).celRules).To(ConsistOf(celRule{
			rule:    "self <= 0 || self >= 1000",
			message: "must be <= 0 or >= 1000",
		}))
	})

	It("keeps the minimum of the type when the rules give none", func() {
		numbers := numberRange{upper: &bound{value: 65535}}

		c := numbers.constraints(typeBounds{min: new(int64(0))})

		Expect(c.markers).To(Equal([]string{
			"kubebuilder:validation:Minimum=0",
			"kubebuilder:validation:Maximum=65535",
		}))
		Expect(c.zeroValid).To(BeTrue())
	})

	It("refuses zero under an exclusive minimum of zero", func() {
		numbers := numberRange{lower: &bound{value: 0, exclusive: true}}

		c := numbers.constraints(typeBounds{})

		Expect(c.zeroValid).To(BeFalse())
		Expect(c.markers).To(ContainElement("kubebuilder:validation:ExclusiveMinimum=true"))
	})
})
