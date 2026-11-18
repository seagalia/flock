package instance

import (
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

func (x *Instance) Signature() (string, error) {
	return helpers.MessageSignature(x)
}

func (x *Instance) GetIdentifier() string {
	return x.GetUUID()
}

func (x *Instance) SetIdentifier(identifier string) {
	x.UUID = identifier
}

func (x *Instance) ForeignResources() map[string][]string {
	return map[string][]string{
		helpers.TypeUrl[*vip.VIP]():   x.GetVipsUuid(),
		helpers.TypeUrl[*zone.Zone](): {x.GetZoneUuid()},
	}
}

func (x *Instance) AddMetadata(key, value string) {
	if x.Metadata == nil {
		x.Metadata = make(map[string]string)
	}
	x.Metadata[key] = value
}

func (x *Instance) DeleteMetadata(key string) map[string]string {
	if x.Metadata == nil {
		return nil
	}
	delete(x.Metadata, key)
	return x.Metadata
}

func (x *Instance) SetMetadata(m map[string]string) {
	x.Metadata = m
}
