package common

import (
	"errors"
	"fmt"
	"regexp"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Errors", func() {
	Describe("CodeName", func() {
		DescribeTable("gives the grpc name of a code",
			func(code connect.Code, name string) {
				Expect(CodeName(code)).To(Equal(name))
			},
			Entry(nil, connect.Code(0), "OK"),
			Entry(nil, connect.CodeCanceled, "Canceled"),
			Entry(nil, connect.CodeUnknown, "Unknown"),
			Entry(nil, connect.CodeInvalidArgument, "InvalidArgument"),
			Entry(nil, connect.CodeDeadlineExceeded, "DeadlineExceeded"),
			Entry(nil, connect.CodeNotFound, "NotFound"),
			Entry(nil, connect.CodeAlreadyExists, "AlreadyExists"),
			Entry(nil, connect.CodePermissionDenied, "PermissionDenied"),
			Entry(nil, connect.CodeResourceExhausted, "ResourceExhausted"),
			Entry(nil, connect.CodeFailedPrecondition, "FailedPrecondition"),
			Entry(nil, connect.CodeAborted, "Aborted"),
			Entry(nil, connect.CodeOutOfRange, "OutOfRange"),
			Entry(nil, connect.CodeUnimplemented, "Unimplemented"),
			Entry(nil, connect.CodeInternal, "Internal"),
			Entry(nil, connect.CodeUnavailable, "Unavailable"),
			Entry(nil, connect.CodeDataLoss, "DataLoss"),
			Entry(nil, connect.CodeUnauthenticated, "Unauthenticated"),
		)
	})

	Describe("StringToStatusCode", func() {
		It("maps every grpc code name to its code", func() {
			for code := connect.Code(0); code <= connect.CodeUnauthenticated; code++ {
				Expect(StringToStatusCode(CodeName(code))).To(Equal(code))
			}
		})

		It("falls back to Unknown", func() {
			Expect(StringToStatusCode("NotACode")).To(Equal(connect.CodeUnknown))
		})
	})

	Describe("WrapError", func() {
		It("keeps the connect code and prefixes the message", func() {
			err := WrapError("loading", connect.NewError(connect.CodeNotFound, "vip missing"))

			var connectErr *connect.Error
			Expect(errors.As(err, &connectErr)).To(BeTrue())
			Expect(connectErr.Code()).To(Equal(connect.CodeNotFound))
			Expect(connectErr.Message()).To(Equal("loading: vip missing"))
		})

		It("wraps a plain error", func() {
			base := errors.New("boom")

			err := WrapError("loading", base)

			Expect(err).To(MatchError(base))
			Expect(err.Error()).To(Equal("loading: boom"))
		})
	})

	Describe("RpcErrorFormat", func() {
		It("returns non rpc errors unchanged", func() {
			plain := errors.New("plain")

			Expect(RpcErrorFormat(plain)).To(BeIdenticalTo(plain))
		})

		It("prints codes like before", func() {
			err := RpcErrorFormat(connect.NewError(connect.CodeNotFound, "vs%20x"))

			withoutColors := regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(err.Error(), "")
			Expect(withoutColors).To(Equal("vs x (Code: NotFound)"))
		})
	})

	Describe("RpcErrorFromString", func() {
		DescribeTable("parses rpc error strings",
			func(input string, wantCode connect.Code, wantMsg string) {
				var connectErr *connect.Error
				Expect(errors.As(RpcErrorFromString(input), &connectErr)).To(BeTrue())
				Expect(connectErr.Code()).To(Equal(wantCode))
				Expect(connectErr.Message()).To(Equal(wantMsg))
			},
			Entry("unimplemented is normalized",
				"rpc error: code = Unimplemented desc = unknown method Foo", connect.CodeUnimplemented, "Unimplemented feature"),
			Entry("rpc error is parsed",
				"rpc error: code = NotFound desc = backend not found", connect.CodeNotFound, "backend not found"),
			Entry("agent uuid suffix is removed",
				"rpc error: code = Internal desc = agent failed UUID: 1234", connect.CodeInternal, "agent failed"),
		)

		It("parses status errors written by the flock agent", func() {
			// the agent's ads handlers store fmt.Errorf("failed to add: %w", err) as the resource status error
			handlerErr := connect.NewError(connect.CodeFailedPrecondition, "vip 10.0.0.1 is not announced UUID: 1234")
			statusError := fmt.Errorf("failed to add: %w", handlerErr).Error()

			var connectErr *connect.Error
			Expect(errors.As(RpcErrorFromString(statusError), &connectErr)).To(BeTrue())
			Expect(connectErr.Code()).To(Equal(connect.CodeFailedPrecondition))
			Expect(connectErr.Message()).To(Equal("vip 10.0.0.1 is not announced"))
		})

		It("normalizes an unimplemented error written by the flock agent", func() {
			statusError := fmt.Errorf("failed to add: %w", connect.NewError(connect.CodeUnimplemented, "no handler")).Error()

			var connectErr *connect.Error
			Expect(errors.As(RpcErrorFromString(statusError), &connectErr)).To(BeTrue())
			Expect(connectErr.Code()).To(Equal(connect.CodeUnimplemented))
			Expect(connectErr.Message()).To(Equal("Unimplemented feature"))
		})

		It("keeps a plain status error written by the flock agent as is", func() {
			statusError := fmt.Errorf("failed to add: %w", errors.New("cannot bind 10.0.0.1")).Error()

			err := RpcErrorFromString(statusError)

			var connectErr *connect.Error
			Expect(errors.As(err, &connectErr)).To(BeFalse())
			Expect(err).To(MatchError("failed to add: cannot bind 10.0.0.1"))
		})

		It("keeps a non rpc message as is, even with a percent sign", func() {
			err := RpcErrorFromString("disk 100% full")

			var connectErr *connect.Error
			Expect(errors.As(err, &connectErr)).To(BeFalse())
			Expect(err).To(MatchError("disk 100% full"))
		})
	})
})
