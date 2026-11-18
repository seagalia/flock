package logging_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/sdk/logging"
)

var _ = Describe("HTTPServerErrorLog", func() {
	var buf *bytes.Buffer

	BeforeEach(func() {
		buf = &bytes.Buffer{}
		Expect(Setup(Options{Level: "debug", InJson: true, Console: buf})).To(Succeed())
	})

	AfterEach(func() {
		Expect(Setup(Options{Console: GinkgoWriter})).To(Succeed())
	})

	DescribeTable("logs the http server errors in zerolog",
		func(line, level string) {
			HTTPServerErrorLog("server").Print(line)

			lines := jsonLines(buf)
			Expect(lines).To(HaveLen(1))
			Expect(lines[0]).To(HaveKeyWithValue("level", level))
			Expect(lines[0]).To(HaveKeyWithValue("component", "server"))
			Expect(lines[0]).To(HaveKeyWithValue("message", line))
		},
		Entry("a client closing before the tls handshake, like a tcp health check, in debug",
			"http: TLS handshake error from 127.0.0.1:59439: EOF", "debug"),
		Entry("a failed tls handshake in warn",
			"http: TLS handshake error from 127.0.0.1:59439: remote error: tls: bad certificate", "warn"),
		Entry("other errors in error",
			"http: panic serving 127.0.0.1:59439: boom", "error"),
	)
})
