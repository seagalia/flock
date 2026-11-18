package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("help", func() {
	It("prints the usage with every command", func() {
		out, err := newCommandRunner().run("help")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Usage: flock <command>"))
		Expect(out).To(ContainSubstring("create-vip"))
		Expect(out).To(ContainSubstring("status-agents"))
	})
})
