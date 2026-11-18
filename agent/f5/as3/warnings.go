package as3

import (
	"slices"

	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

type ResourceKey struct {
	TypeUrl string
	UUID    string
}

func KeyOf(m proto.Message) ResourceKey {
	return ResourceKey{TypeUrl: helpers.TypeUrlFromMessage(m), UUID: helpers.GetIdentifier(m)}
}

func (r *Result) warn(m proto.Message, warning string) {
	key := KeyOf(m)
	if slices.Contains(r.Warnings[key], warning) {
		return
	}
	r.Warnings[key] = append(r.Warnings[key], warning)
	slices.Sort(r.Warnings[key])
}

func (r *Result) fail(m proto.Message, err string) {
	r.Errors[KeyOf(m)] = err
}

func (r *Result) missing(from proto.Message, ref ResourceKey) {
	key := KeyOf(from)
	if slices.Contains(r.Missing[key], ref) {
		return
	}
	r.Missing[key] = append(r.Missing[key], ref)
}
