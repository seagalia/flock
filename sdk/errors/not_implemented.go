package errors

import (
	"fmt"

	"connectrpc.com/connect/v2"
)

type ErrNotImplemented struct {
	funcName string
}

func NewErrNotImplemented(funcName string) *ErrNotImplemented {
	return &ErrNotImplemented{
		funcName: funcName,
	}
}

func (e ErrNotImplemented) Error() string {
	return fmt.Sprintf("function %s not implemented", e.funcName)
}

func IsErrNotImplemented(err error) bool {
	_, ok := err.(*ErrNotImplemented)
	if ok {
		return true
	}
	return connect.CodeOf(err) == connect.CodeUnimplemented
}
