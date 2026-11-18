package errors

import (
	errpkg "errors"
	"fmt"

	"connectrpc.com/connect/v2"
)

type ErrAlreadyExists struct {
	elemName string
	elemKey  string
}

func NewErrAlreadyExists(elemName, elemKey string) *ErrAlreadyExists {
	return &ErrAlreadyExists{
		elemName: elemName,
		elemKey:  elemKey,
	}
}

func (e ErrAlreadyExists) Error() string {
	return fmt.Sprintf("%s with key %s already exists", e.elemName, e.elemKey)
}

func IsErrAlreadyExists(err error) bool {
	var errAlreadyExists *ErrAlreadyExists
	ok := errpkg.As(err, &errAlreadyExists)
	if ok {
		return true
	}
	return connect.CodeOf(err) == connect.CodeAlreadyExists
}
