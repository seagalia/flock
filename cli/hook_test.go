package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("hook", func() {
	It("prints a script registering the binary as completer", func() {
		out, err := newCommandRunner().run("hook", "--completion", "bash")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(MatchRegexp(`complete -o default -C ".+" flock`))
	})

	It("enables bash completion in zsh first", func() {
		script := (&Hook{}).makeCompletion(" ZSH ", "/usr/local/bin/flock")

		Expect(script).To(Equal("autoload -U +X bashcompinit && bashcompinit\ncomplete -o default -C \"/usr/local/bin/flock\" flock\n"))
	})

	It("gives the bash script by default", func() {
		script := (&Hook{}).makeCompletion("fish", "/usr/local/bin/flock")

		Expect(script).To(Equal("complete -o default -C \"/usr/local/bin/flock\" flock\n"))
	})
})
