package cli

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("readSecretData", func() {
	It("reads key=value pairs", func() {
		data, err := readSecretData([]string{"a=1", "b=x=y"}, nil, strings.NewReader(""))

		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(Equal(map[string]string{"a": "1", "b": "x=y"}))
	})

	It("reads a value from stdin with =- and drops its trailing newline", func() {
		data, err := readSecretData([]string{"a=-"}, nil, strings.NewReader("piped\n"))

		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(Equal(map[string]string{"a": "piped"}))
	})

	It("reads a value from a file", func() {
		data, err := readSecretData(nil, []string{"a=" + writeTempFile("a.txt", "from file\n")}, strings.NewReader(""))

		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(Equal(map[string]string{"a": "from file\n"}))
	})

	It("refuses the same key twice, whatever its source", func() {
		_, err := readSecretData([]string{"a=1"}, []string{"a=" + writeTempFile("a.txt", "x")}, strings.NewReader(""))

		Expect(err).To(MatchError("key a is given twice"))
	})

	It("refuses an empty value, from a pair, stdin or a file", func() {
		_, err := readSecretData([]string{"a="}, nil, strings.NewReader(""))
		Expect(err).To(MatchError("key a has an empty value"))

		_, err = readSecretData([]string{"a=-"}, nil, strings.NewReader("\n"))
		Expect(err).To(MatchError("key a has an empty value"))

		_, err = readSecretData(nil, []string{"a=" + writeTempFile("a.txt", "")}, strings.NewReader(""))
		Expect(err).To(MatchError("key a has an empty value"))
	})

	It("refuses two keys reading stdin", func() {
		_, err := readSecretData([]string{"a=-", "b=-"}, nil, strings.NewReader("x"))

		Expect(err).To(MatchError(ContainSubstring("only one key can read stdin")))
	})

	It("never echoes a value in an error", func() {
		_, err := readSecretData([]string{"hunter2"}, nil, strings.NewReader(""))

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).NotTo(ContainSubstring("hunter2"))
	})
})
