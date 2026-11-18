package loghooks_test

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/leodido/go-syslog/v4/rfc5424"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/agentsdk/loghooks"
)

type closingBuffer struct {
	bytes.Buffer
}

func (b *closingBuffer) Close() error {
	return nil
}

const errorLine = `{"level":"error","component":"aggregator","time":"2026-01-02T03:04:05Z","message":"first line\nsecond line"}`

var _ = Describe("SyslogWriter", func() {
	var (
		buf            *closingBuffer
		structuredData map[string]map[string]string
	)

	BeforeEach(func() {
		buf = &closingBuffer{}
		structuredData = map[string]map[string]string{"meta@1": {"env": "test"}}
	})

	write := func(rfc3164 bool, line string) {
		writer := NewSyslogWriter(buf, "host-1", "flock-agent", rfc3164, structuredData)
		_, err := writer.Write([]byte(line + "\n"))
		Expect(err).ToNot(HaveOccurred())
	}

	It("writes rfc5424 messages with fields as structured data", func() {
		write(false, errorLine)

		parsed, err := rfc5424.NewParser().Parse(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
		Expect(err).ToNot(HaveOccurred())
		message := parsed.(*rfc5424.SyslogMessage)
		Expect(*message.Hostname).To(Equal("host-1"))
		Expect(*message.Appname).To(Equal("flock-agent"))
		Expect(*message.ProcID).To(Equal(strconv.Itoa(os.Getpid())))
		Expect(*message.MsgID).To(Equal("flock-agent-log"))
		Expect(*message.Severity).To(BeEquivalentTo(3))
		Expect(*message.Message).To(Equal("first line second line"))
		flockData := (*message.StructuredData)["flock"]
		Expect(flockData).To(HaveKeyWithValue("component", "aggregator"))
		Expect(flockData).ToNot(HaveKey("level"))
		Expect(flockData).ToNot(HaveKey("time"))
		Expect(flockData).ToNot(HaveKey("message"))
		Expect((*message.StructuredData)["meta@1"]).To(HaveKeyWithValue("env", "test"))
	})

	It("uses msg_id field as message id", func() {
		write(false, `{"level":"info","msg_id":"custom","message":"access log"}`)
		Expect(buf.String()).To(ContainSubstring(" custom "))
		Expect(buf.String()).ToNot(ContainSubstring("msg_id"))
	})

	It("writes rfc3164 messages on one line", func() {
		write(true, errorLine)
		Expect(buf.String()).To(HavePrefix(fmt.Sprintf("<11>2026-01-02T03:04:05Z host-1 flock-agent[%d]: ", os.Getpid())))
		Expect(buf.String()).To(ContainSubstring("first line second line component=aggregator"))
		Expect(strings.Count(buf.String(), "\n")).To(Equal(1), "only the final new line is kept")
		Expect(buf.String()).To(HaveSuffix("\n"))
	})

	DescribeTable("maps levels to severities",
		func(level, expectedPriority string) {
			write(true, fmt.Sprintf(`{"level":%q,"message":"hello"}`, level))
			Expect(buf.String()).To(HavePrefix(expectedPriority))
		},
		Entry("panic", "panic", "<9>"),
		Entry("fatal", "fatal", "<10>"),
		Entry("error", "error", "<11>"),
		Entry("warn", "warn", "<12>"),
		Entry("info", "info", "<14>"),
		Entry("debug", "debug", "<15>"),
		Entry("trace", "trace", "<15>"),
	)

	It("ignores lines without message", func() {
		write(false, `{"level":"info","component":"envoy"}`)
		Expect(buf.Len()).To(BeZero())
	})
})
