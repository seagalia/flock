package cli

import (
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("logs", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("prints the latest log lines of every agent with --latest", func() {
		runner.conn.Handle(svclookupconnect.LookupGetLogsLatestProcedure, reply(&svclookup.LogsResponse{
			Logs: []*svclookup.LogsResponse_LogResponse{
				{AgentUuid: "agent-1", LogLines: []string{"started\n", "ready\n"}},
				{AgentUuid: "agent-2", LogLines: []string{"failed\n"}},
			},
		}))

		out, err := runner.run("logs", "--latest", "--agent", "agent-1", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Agent agent-1    started"))
		Expect(out).To(ContainSubstring("Agent agent-1    ready"))
		Expect(out).To(ContainSubstring("Agent agent-2    failed"))
		sent := lastRequest[*svclookup.GetLogsRequest](runner.conn, svclookupconnect.LookupGetLogsLatestProcedure)
		Expect(sent.GetAgentUuid()).To(Equal("agent-1"))
	})

	It("prints the lines of a provisioner like the ones of an agent", func() {
		runner.conn.Handle(svclookupconnect.LookupGetLogsLatestProcedure, reply(&svclookup.LogsResponse{
			Logs: []*svclookup.LogsResponse_LogResponse{
				{AgentUuid: "agent-1", LogLines: []string{"serving\n"}},
				{AgentUuid: "acme-1", LogLines: []string{"order pending\n"}},
			},
		}))

		out, err := runner.run("logs", "--latest", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Agent acme-1    order pending"))
		Expect(out).To(ContainSubstring("Agent agent-1    serving"))
	})

	It("follows the log stream until the server closes it", func() {
		runner.conn.HandleStream(svclookupconnect.LookupGetLogsStreamProcedure, func(proto.Message) ([]proto.Message, error) {
			return []proto.Message{
				&svclookup.LogsStreamResponse{Event: &svclookup.LogsStreamResponse_LogStream{LogStream: &svclookup.LogsStreamResponse_AgentLogLine{AgentUuid: "agent-1", LogLine: "first\n"}}},
				&svclookup.LogsStreamResponse{Event: &svclookup.LogsStreamResponse_LogStream{LogStream: &svclookup.LogsStreamResponse_AgentLogLine{AgentUuid: "agent-1", LogLine: "second\n"}}},
			}, nil
		})

		out, err := runner.run("logs", resourceUuid)

		// The command only stops on a stream error, a closed stream ends with io.EOF.
		Expect(errors.Is(err, io.EOF)).To(BeTrue())
		Expect(out).To(ContainSubstring("Following logs of instance"))
		Expect(out).To(ContainSubstring("first"))
		Expect(out).To(ContainSubstring("second"))
	})

	It("returns without error when its context is cancelled while following", func() {
		runner.conn.HandleStream(svclookupconnect.LookupGetLogsStreamProcedure, func(proto.Message) ([]proto.Message, error) {
			// Ctrl-C comes while the stream is open and quiet
			runner.cancel()
			return nil, nil
		}).KeepStreamOpen(svclookupconnect.LookupGetLogsStreamProcedure)

		_, err := runner.run("logs", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
	})
})
