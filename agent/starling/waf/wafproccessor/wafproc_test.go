package wafproccessor

import (
	"net/http"

	"github.com/corazawaf/coraza/v3/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WafProc", func() {
	DescribeTable("ResponseInterruption answers envoy with",
		func(interruption *types.Interruption, expectedStatus int, expectedLocation string) {
			wafProc := &WafProc{interruption: interruption}

			resp, interrupted := wafProc.ResponseInterruption()

			Expect(interrupted).To(BeTrue())
			Expect(int(resp.ImmediateResponse.GetStatus().GetCode())).To(Equal(expectedStatus))
			Expect(string(resp.ImmediateResponse.GetBody())).To(Equal(http.StatusText(expectedStatus)))
			if expectedLocation == "" {
				Expect(resp.ImmediateResponse.GetHeaders()).To(BeNil())
				return
			}
			setHeader := resp.ImmediateResponse.GetHeaders().GetSetHeaders()[0].GetHeader()
			Expect(setHeader.GetKey()).To(Equal("Location"))
			Expect(string(setHeader.GetRawValue())).To(Equal(expectedLocation))
		},
		Entry("the status of a deny", &types.Interruption{Action: "deny", Status: 406}, 406, ""),
		Entry("403 for a deny without status", &types.Interruption{Action: "deny"}, http.StatusForbidden, ""),
		Entry("the redirection status of a redirect",
			&types.Interruption{Action: "redirect", Status: 307, Data: "https://example.com"}, 307, "https://example.com"),
		Entry("302 for a redirect with a status that can't redirect",
			&types.Interruption{Action: "redirect", Status: 200, Data: "https://example.com"}, http.StatusFound,
			"https://example.com"),
	)

	It("does not interrupt without interruption", func() {
		_, interrupted := (&WafProc{}).ResponseInterruption()
		Expect(interrupted).To(BeFalse())
	})
})
