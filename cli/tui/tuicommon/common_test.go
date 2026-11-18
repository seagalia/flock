package tuicommon

import (
	"errors"
	"strings"

	"charm.land/lipgloss/v2"
	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Common", func() {
	Describe("RpcErrorFormat", func() {
		It("returns the message of a plain error", func() {
			Expect(RpcErrorFormat(errors.New("plain"))).To(Equal("plain"))
		})

		It("shows the unescaped message and the code of a connect error", func() {
			got := RpcErrorFormat(connect.NewError(connect.CodeNotFound, "vip%20missing"))

			Expect(got).To(ContainSubstring("vip missing"))
			Expect(got).To(ContainSubstring("(Code: "))
			Expect(got).To(ContainSubstring("NotFound"))
		})
	})

	Describe("FormatToWidth", func() {
		It("keeps a short string unchanged", func() {
			Expect(FormatToWidth("short", 10)).To(Equal("short"))
		})

		It("only inserts line breaks so every line is narrower than width", func() {
			got := FormatToWidth("abcdefghij", 4)

			Expect(strings.ReplaceAll(got, "\n", "")).To(Equal("abcdefghij"))
			for line := range strings.SplitSeq(got, "\n") {
				Expect(lipgloss.Width(line)).To(BeNumerically("<", 4))
			}
		})
	})
})
