package highlight

import (
	"regexp"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func highlightPlain(input string) string {
	GinkgoHelper()
	out, err := Highlight(strings.NewReader(input))
	Expect(err).NotTo(HaveOccurred())
	return ansiEscape.ReplaceAllString(out, "")
}

var _ = Describe("Highlight", func() {
	It("keeps the yaml content once colors are removed", func() {
		input := strings.Join([]string{
			"name: web",
			"port: 443",
			"ip: 10.0.0.1",
			"enabled: true",
			"# a comment",
			"domains:",
			"- example.com",
			"https://example.com",
		}, "\n")

		Expect(highlightPlain(input)).To(Equal(strings.Join([]string{
			"name: web",
			"port: 443",
			"ip: 10.0.0.1",
			"enabled: true",
			"# a comment",
			"domains: ",
			"- example.com",
			"https://example.com",
		}, "\n") + "\n"))
	})

	It("keeps empty lines in the output", func() {
		Expect(highlightPlain("a: 1\n\nb: 2")).To(Equal("a: 1\n\nb: 2\n"))
	})

	It("keeps key value comments as written", func() {
		Expect(highlightPlain("  # port: 80")).To(Equal("  # port: 80\n"))
	})

	It("keeps lines of a multiline block raw", func() {
		// "echo: hello" looks like key: value but belongs to the block.
		input := "script: |\n  echo: hello\n  exit 0\nnext: value"

		Expect(highlightPlain(input)).To(Equal(input + "\n"))
	})

	It("stops at an EOF line", func() {
		Expect(highlightPlain("a: 1\nEOF\nb: 2")).To(Equal("a: 1\n"))
	})
})
