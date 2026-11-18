package cplters

import (
	"encoding/json"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func ProtoToMap(msg proto.Message) (map[string]any, error) {
	data, err := protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: false,
	}.Marshal(msg)

	if err != nil {
		return nil, err
	}
	mapProto := make(map[string]any)
	err = json.Unmarshal(data, &mapProto)
	if err != nil {
		return nil, err
	}
	return mapProto, nil
}
