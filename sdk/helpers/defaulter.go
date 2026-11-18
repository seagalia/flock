package helpers

import "google.golang.org/protobuf/proto"

type Defaulter interface {
	ProvDefault()
}

func ProvDefault(m proto.Message) {
	defaulter, ok := m.(Defaulter)
	if ok {
		defaulter.ProvDefault()
	}
}
