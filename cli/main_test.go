package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CLI", func() {
	It("fails on an unknown command", func() {
		_, err := newCommandRunner().run("unknown-command")

		Expect(err).To(MatchError(ContainSubstring("unexpected argument unknown-command")))
	})

	It("runs commands that need no client without connecting", func() {
		runner := newCommandRunner()

		_, err := runner.run("hook")

		Expect(err).NotTo(HaveOccurred())
		Expect(runner.conn.Calls()).To(BeEmpty())
	})
})

var _ = Describe("ExpandHome", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("HOME", "/home/someone")
	})

	It("replaces a leading ~ by the home directory", func() {
		Expect(ExpandHome("~/.flock/config.json")).To(Equal("/home/someone/.flock/config.json"))
		Expect(ExpandHome("~")).To(Equal("/home/someone"))
	})

	It("keeps a path without leading ~", func() {
		Expect(ExpandHome("/etc/flock/config.json")).To(Equal("/etc/flock/config.json"))
		Expect(ExpandHome("~other/config.json")).To(Equal("~other/config.json"))
	})
})
