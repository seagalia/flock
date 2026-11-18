package helpers

import (
	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
)

// Validate checks the protovalidate rules declared in the protos
func Validate(message proto.Message) error {
	if message == nil {
		return nil
	}
	return protovalidate.Validate(message)
}
