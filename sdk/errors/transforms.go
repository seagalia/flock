package errors

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"
)

func ToConnectError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(connect.CodeDeadlineExceeded, err.Error())
	}
	if IsErrNotFound(err) {
		return connect.NewError(connect.CodeNotFound, err.Error())
	}
	if IsErrNotImplemented(err) {
		return connect.NewError(connect.CodeUnimplemented, err.Error())
	}
	if IsErrAlreadyExists(err) {
		return connect.NewError(connect.CodeAlreadyExists, err.Error())
	}
	return connect.NewError(connect.CodeInternal, err.Error())
}
