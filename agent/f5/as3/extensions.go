package as3

import (
	"fmt"
	"reflect"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// unpackOptions gives the F5 options packed in a typed_options field, an empty message when the field is empty or
// holds the extension of another agent. That one is skipped without a warning, because a resource served by several
// agents carries the options of each of them.
func unpackOptions[T proto.Message](res *Result, on proto.Message, packed *anypb.Any) T {
	var opts T
	opts = reflect.New(reflect.TypeOf(opts).Elem()).Interface().(T)
	if packed == nil || !packed.MessageIs(opts) {
		return opts
	}
	err := packed.UnmarshalTo(opts)
	if err != nil {
		res.warn(on, fmt.Sprintf("extension %s can't be read, it is ignored: %s", packed.MessageName(), err))
	}
	return opts
}
