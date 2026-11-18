package helpers

import (
	"bytes"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func MarshalListProtoMessage[T proto.Message](opts protojson.MarshalOptions, messages []T) ([]byte, error) {
	var err error
	elems := make([][]byte, len(messages))
	for i, m := range messages {
		elems[i], err = opts.Marshal(m)
		if err != nil {
			return nil, err
		}
	}
	joinedElems := bytes.Join(elems, []byte(","))
	final := make([]byte, len(joinedElems)+2)
	final[0] = '['
	copy(final[1:], joinedElems)
	final[len(final)-1] = ']'

	return final, nil
}
