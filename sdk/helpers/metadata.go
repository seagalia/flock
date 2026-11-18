package helpers

import "google.golang.org/protobuf/proto"

type MetadataMutable interface {
	GetMetadata() map[string]string
	AddMetadata(key, value string)
	DeleteMetadata(key string) map[string]string
	SetMetadata(m map[string]string)
}

func GetMetadata(m proto.Message) map[string]string {
	if m, ok := m.(MetadataMutable); ok {
		return m.GetMetadata()
	}
	return make(map[string]string)
}
