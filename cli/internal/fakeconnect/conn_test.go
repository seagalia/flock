package fakeconnect_test

import (
	"context"
	"errors"
	"io"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	"github.com/seagalia/flock/cli/internal/fakeconnect"
	vs "github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Conn", func() {
	var (
		conn *fakeconnect.Conn
		ctx  context.Context
	)

	BeforeEach(func() {
		conn = fakeconnect.New()
		ctx = context.Background()
	})

	readVS := func() (*svcvs.VSResponse, error) {
		return svcvsconnect.NewVirtualServerClient(conn.Client()).ReadVS(ctx, &svccore.UuidRequest{UUID: "u1"})
	}

	It("answers a unary call with the registered handler", func() {
		conn.Handle(svcvsconnect.VirtualServerReadVSProcedure, func(proto.Message) (proto.Message, error) {
			return &svcvs.VSResponse{Vs: &vs.VirtualServer{UUID: "u1"}}, nil
		})

		resp, err := readVS()

		Expect(err).ToNot(HaveOccurred())
		Expect(resp.GetVs().GetUUID()).To(Equal("u1"))
		Expect(conn.Calls()).To(Equal([]string{svcvsconnect.VirtualServerReadVSProcedure}))
		requests := conn.Requests(svcvsconnect.VirtualServerReadVSProcedure)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].(*svccore.UuidRequest).GetUUID()).To(Equal("u1"))
	})

	It("returns the handler error as is", func() {
		conn.Handle(svcvsconnect.VirtualServerReadVSProcedure, func(proto.Message) (proto.Message, error) {
			return nil, connect.NewError(connect.CodeNotFound, "nope")
		})

		_, err := readVS()

		Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
	})

	It("answers unimplemented for an unregistered procedure", func() {
		_, err := readVS()

		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnimplemented))
		Expect(conn.Calls()).To(Equal([]string{svcvsconnect.VirtualServerReadVSProcedure}))
		requests := conn.Requests(svcvsconnect.VirtualServerReadVSProcedure)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].(*svccore.UuidRequest).GetUUID()).To(Equal("u1"))
	})

	It("answers unimplemented for an unregistered stream without recording it", func() {
		stream, err := svclookupconnect.NewLookupClient(conn.Client()).GetLogsStream(ctx, &svclookup.GetLogsRequest{InstanceUuid: "i"})
		if err == nil {
			_, err = stream.Receive()
		}

		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnimplemented))
		Expect(conn.Calls()).To(BeEmpty())
	})

	It("streams every message of a stream handler then ends", func() {
		conn.HandleStream(svclookupconnect.LookupGetLogsStreamProcedure, func(proto.Message) ([]proto.Message, error) {
			return []proto.Message{
				&svclookup.LogsStreamResponse{Event: &svclookup.LogsStreamResponse_LogStream{LogStream: &svclookup.LogsStreamResponse_AgentLogLine{AgentUuid: "a", LogLine: "one"}}},
				&svclookup.LogsStreamResponse{Event: &svclookup.LogsStreamResponse_LogStream{LogStream: &svclookup.LogsStreamResponse_AgentLogLine{AgentUuid: "a", LogLine: "two"}}},
			}, nil
		})

		stream, err := svclookupconnect.NewLookupClient(conn.Client()).GetLogsStream(ctx, &svclookup.GetLogsRequest{InstanceUuid: "i"})
		Expect(err).ToNot(HaveOccurred())

		first, err := stream.Receive()
		Expect(err).ToNot(HaveOccurred())
		Expect(first.GetLogStream().GetLogLine()).To(Equal("one"))
		second, err := stream.Receive()
		Expect(err).ToNot(HaveOccurred())
		Expect(second.GetLogStream().GetLogLine()).To(Equal("two"))
		_, err = stream.Receive()
		Expect(errors.Is(err, io.EOF)).To(BeTrue())
		Expect(conn.Requests(svclookupconnect.LookupGetLogsStreamProcedure)).To(HaveLen(1))
	})

	It("records request headers set by client interceptors", func() {
		conn.Handle(svcvsconnect.VirtualServerReadVSProcedure, func(proto.Message) (proto.Message, error) {
			return &svcvs.VSResponse{}, nil
		})
		asUser := func(next connect.ClientFunc) connect.ClientFunc {
			return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
				callInfo, _ := connect.CallInfoForClientContext(ctx)
				callInfo.RequestHeader().Set("as_user", "x")
				return next(ctx, spec)
			}
		}

		_, err := svcvsconnect.NewVirtualServerClient(conn.Client(asUser)).ReadVS(ctx, &svccore.UuidRequest{UUID: "u1"})

		Expect(err).ToNot(HaveOccurred())
		headers := conn.RequestHeaders(svcvsconnect.VirtualServerReadVSProcedure)
		Expect(headers).To(HaveLen(1))
		Expect(headers[0].Get("as_user")).To(Equal("x"))
	})
})
