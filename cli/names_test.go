package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Names", func() {
	DescribeTable("isUuid",
		func(value string, want bool) {
			Expect(isUuid(value)).To(Equal(want))
		},
		Entry("lowercase uuid", "0f8fad5b-d9cb-469f-a165-70867728950e", true),
		Entry("uppercase uuid", "0F8FAD5B-D9CB-469F-A165-70867728950E", true),
		Entry("name", "backend", false),
		Entry("too long", "0f8fad5b-d9cb-469f-a165-70867728950ef", false),
		Entry("right length, not hex", "zzzzzzzz-d9cb-469f-a165-70867728950e", false),
	)
})
