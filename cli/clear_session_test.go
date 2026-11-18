package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("clear-session", func() {
	It("confirms the session is cleared", func() {
		out, err := newCommandRunner().run("clear-session")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Session and token cleared"))
	})
})
