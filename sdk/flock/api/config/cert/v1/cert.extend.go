package cert

import "github.com/seagalia/flock/sdk/helpers"

func (x *Certificate) Signature() (string, error) {
	return helpers.MessageSignature(x)
}

func (x *Certificate) GetIdentifier() string {
	return x.GetUUID()
}

func (x *Certificate) SetIdentifier(identifier string) {
	x.UUID = identifier
}

func (x *Certificate) AddMetadata(key, value string) {
	if x.Metadata == nil {
		x.Metadata = make(map[string]string)
	}
	x.Metadata[key] = value
}

func (x *Certificate) DeleteMetadata(key string) map[string]string {
	if x.Metadata == nil {
		return nil
	}
	delete(x.Metadata, key)
	return x.Metadata
}

func (x *Certificate) SetMetadata(m map[string]string) {
	x.Metadata = m
}
