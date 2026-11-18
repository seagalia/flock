package logextends_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"

	. "github.com/seagalia/flock/agent/starling/logextends"
)

var _ = Describe("MemberlistWriter", func() {
	DescribeTable("converts memberlist prefixes to levels",
		func(line, expectedMessage, expectedLevel string) {
			buf := &bytes.Buffer{}
			writer := &MemberlistWriter{Logger: zerolog.New(buf)}
			_, err := writer.Write([]byte(line + "\n"))
			Expect(err).ToNot(HaveOccurred())
			decoded := decodeLines(buf)
			Expect(decoded).To(HaveLen(1))
			Expect(decoded[0]).To(HaveKeyWithValue("message", expectedMessage))
			Expect(decoded[0]).To(HaveKeyWithValue("level", expectedLevel))
		},
		Entry("error", "[ERR] memberlist: failed", "memberlist: failed", "error"),
		Entry("warn", "[WARN] memberlist: slow", "memberlist: slow", "warn"),
		Entry("debug", "[DEBUG] memberlist: stream", "memberlist: stream", "debug"),
		Entry("no prefix", "memberlist: info", "memberlist: info", "info"),
		Entry("info prefix is kept", "[INFO] memberlist: joined", "[INFO] memberlist: joined", "info"),
	)
})
