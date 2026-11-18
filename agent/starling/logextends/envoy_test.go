package logextends_test

import (
	"bytes"
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"

	. "github.com/seagalia/flock/agent/starling/logextends"
)

func decodeLines(buf *bytes.Buffer) []map[string]any {
	decoded := make([]map[string]any, 0)
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		fields := map[string]any{}
		Expect(json.Unmarshal([]byte(line), &fields)).To(Succeed(), line)
		decoded = append(decoded, fields)
	}
	return decoded
}

var _ = Describe("EnvoyLines", func() {
	var buf *bytes.Buffer

	BeforeEach(func() {
		buf = &bytes.Buffer{}
	})

	emit := func(output string, accessLog bool, line string) map[string]any {
		lines := &EnvoyLines{Logger: zerolog.New(buf), Output: output, AccessLog: accessLog}
		lines.Emit(line)
		decoded := decodeLines(buf)
		Expect(decoded).To(HaveLen(1))
		return decoded[0]
	}

	It("explodes json access logs into fields", func() {
		line := emit("stdout", true, `{"method":"GET","response_code":200}`)
		Expect(line).To(HaveKeyWithValue("message", "access log"))
		Expect(line).To(HaveKeyWithValue("method", "GET"))
		Expect(line).To(HaveKeyWithValue("response_code", float64(200)))
		Expect(line).To(HaveKeyWithValue("output", "stdout"))
		Expect(line).To(HaveKeyWithValue("msg_id", "access_log"))
	})

	It("keeps the message of other stdout json lines", func() {
		line := emit("stdout", false, `{"message":"hi","a":"b"}`)
		Expect(line).To(HaveKeyWithValue("message", "hi"))
		Expect(line).To(HaveKeyWithValue("a", "b"))
		Expect(line).ToNot(HaveKey("msg_id"))
	})

	It("keeps stdout broken json as the message", func() {
		line := emit("stdout", true, `{"method":`)
		Expect(line).To(HaveKeyWithValue("message", `{"method":`))
		Expect(line).To(HaveKeyWithValue("level", "info"))
	})

	It("keeps non json lines as is", func() {
		line := emit("stdout", true, `[2024-01-01] GET / 200`)
		Expect(line).To(HaveKeyWithValue("message", `[2024-01-01] GET / 200`))
		Expect(line).To(HaveKeyWithValue("level", "info"))
		buf.Reset()
		line = emit("stderr", false, `plain text line`)
		Expect(line).To(HaveKeyWithValue("message", `plain text line`))
		Expect(line).To(HaveKeyWithValue("output", "stderr"))
	})

	DescribeTable("maps envoy app log levels",
		func(envoyLevel, expectedLevel string) {
			line := emit("stderr", false,
				`{"log_level":"`+envoyLevel+`","message":"started","thread_id":"12","logger_name":"main","file":"server.cc"}`)
			Expect(line).To(HaveKeyWithValue("level", expectedLevel))
			Expect(line).To(HaveKeyWithValue("message", "started"))
			Expect(line).To(HaveKeyWithValue("thread_id", "12"))
			Expect(line).To(HaveKeyWithValue("logger", "main"))
			Expect(line).To(HaveKeyWithValue("file", "server.cc"))
		},
		Entry("critical maps to fatal level without exiting", "critical", "fatal"),
		Entry("error", "error", "error"),
		Entry("warn", "warn", "warn"),
		Entry("info", "info", "info"),
		Entry("debug", "debug", "debug"),
		Entry("trace", "trace", "trace"),
	)

	It("uses info when envoy level is unknown", func() {
		line := emit("stderr", false, `{"log_level":"loud","message":"msg"}`)
		Expect(line).To(HaveKeyWithValue("level", "info"))
		Expect(line).To(HaveKeyWithValue("message", "msg"))
	})

	It("keeps malformed stderr json as the message", func() {
		line := emit("stderr", false, `{"log_level":`)
		Expect(line).To(HaveKeyWithValue("message", `{"log_level":`))
		Expect(line).To(HaveKeyWithValue("level", "info"))
	})

	It("skips empty lines", func() {
		(&EnvoyLines{Logger: zerolog.New(buf), Output: "stdout"}).Emit("")
		Expect(buf.Len()).To(BeZero())
	})

	It("emits a last line without new line on flush", func() {
		lines := &EnvoyLines{Logger: zerolog.New(buf), Output: "stderr"}
		_, err := lines.Write([]byte("a\ncrashed mid line"))
		Expect(err).ToNot(HaveOccurred())
		lines.Flush()
		lines.Flush()
		Expect(decodeLines(buf)).To(HaveExactElements(
			HaveKeyWithValue("message", "a"),
			HaveKeyWithValue("message", "crashed mid line"),
		))
	})

	It("splits writes into lines", func() {
		lines := &EnvoyLines{Logger: zerolog.New(buf), Output: "stderr"}
		_, err := lines.Write([]byte("a\nb"))
		Expect(err).ToNot(HaveOccurred())
		_, err = lines.Write([]byte("c\n"))
		Expect(err).ToNot(HaveOccurred())
		Expect(decodeLines(buf)).To(HaveExactElements(
			HaveKeyWithValue("message", "a"),
			HaveKeyWithValue("message", "bc"),
		))
	})
})
