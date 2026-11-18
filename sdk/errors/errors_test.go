package errors_test

import (
	"context"
	stderrors "errors"
	"fmt"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/errors"
)

var _ = Describe("Errors", func() {
	errOther := fmt.Errorf("other")

	Context("ToConnectError", func() {
		It("should keep a cancellation a cancellation", func() {
			err := ToConnectError(fmt.Errorf("read: %w", context.Canceled))
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeCanceled))
			Expect(err.Error()).To(ContainSubstring("read: context canceled"))
		})
		It("should keep an expired deadline an expired deadline", func() {
			err := ToConnectError(fmt.Errorf("read: %w", context.DeadlineExceeded))
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeDeadlineExceeded))
		})
	})

	Context("ErrNotFound", func() {
		It("should be recognized from itself or a connect not found error", func() {
			err := NewErrNotFound("backend", "backend-1")
			Expect(err.Error()).To(Equal("backend with key backend-1 not found"))
			Expect(IsErrNotFound(err)).To(BeTrue())
			Expect(IsErrNotFound(connect.NewError(connect.CodeNotFound, "not found"))).To(BeTrue())
			Expect(IsErrNotFound(connect.NewError(connect.CodeInternal, "internal"))).To(BeFalse())
			Expect(IsErrNotFound(errOther)).To(BeFalse())
		})
	})

	Context("ErrAlreadyExists", func() {
		It("should be recognized from itself or a connect already exists error", func() {
			err := NewErrAlreadyExists("backend", "backend-1")
			Expect(err.Error()).To(Equal("backend with key backend-1 already exists"))
			Expect(IsErrAlreadyExists(err)).To(BeTrue())
			Expect(IsErrNotFound(err)).To(BeFalse())
			Expect(IsErrAlreadyExists(connect.NewError(connect.CodeAlreadyExists, "exists"))).To(BeTrue())
			Expect(IsErrAlreadyExists(errOther)).To(BeFalse())
		})
	})

	Context("ErrNotImplemented", func() {
		It("should be recognized from itself or a connect unimplemented error", func() {
			err := NewErrNotImplemented("Create")
			Expect(err.Error()).To(Equal("function Create not implemented"))
			Expect(IsErrNotImplemented(err)).To(BeTrue())
			Expect(IsErrNotImplemented(connect.NewError(connect.CodeUnimplemented, "unimplemented"))).To(BeTrue())
			Expect(IsErrNotImplemented(errOther)).To(BeFalse())
		})
	})

	Context("ToConnectError", func() {
		DescribeTable("should convert to the matching connect error",
			func(err error, expectedCode connect.Code) {
				converted := ToConnectError(err)
				Expect(connect.CodeOf(converted)).To(Equal(expectedCode))
				Expect(converted.(*connect.Error).Message()).To(Equal(err.Error()))
			},
			Entry("not found", NewErrNotFound("backend", "c1"), connect.CodeNotFound),
			Entry("not implemented", NewErrNotImplemented("Create"), connect.CodeUnimplemented),
			Entry("already exists", NewErrAlreadyExists("backend", "c1"), connect.CodeAlreadyExists),
			Entry("any other error", fmt.Errorf("boom"), connect.CodeInternal),
			Entry("message with a percent sign", stderrors.New("100%s done"), connect.CodeInternal),
		)

		It("should keep nil", func() {
			Expect(ToConnectError(nil)).To(Succeed())
		})
	})
})
