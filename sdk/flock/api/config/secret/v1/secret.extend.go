package secret

import (
	"fmt"
	"strings"

	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

// RefPrefix starts a metadata value that points at a value of a Secret instead of holding it.
const RefPrefix = "secret://"

// Ref points at the value stored under Key in the secret SecretUUID.
type Ref struct {
	SecretUUID string
	Key        string
}

func (r Ref) String() string {
	return RefPrefix + r.SecretUUID + "/" + r.Key
}

// ParseRef reads a metadata value.
func ParseRef(value string) (ref Ref, isRef bool, err error) {
	rest, hasPrefix := strings.CutPrefix(value, RefPrefix)
	if !hasPrefix {
		return Ref{}, false, nil
	}
	secretUUID, key, hasKey := strings.Cut(rest, "/")
	isWellFormed := hasKey && secretUUID != "" && key != "" && !strings.Contains(key, "/")
	if !isWellFormed {
		return Ref{}, true, fmt.Errorf("%q is not a reference of the form %s<secret uuid>/<key>", value, RefPrefix)
	}
	return Ref{SecretUUID: secretUUID, Key: key}, true, nil
}

// Blanked returns a clone of the secret whose data values are all empty, the shape of a read or list answer.
func (x *Secret) Blanked() *Secret {
	blanked := proto.Clone(x).(*Secret)
	for key := range blanked.Data {
		blanked.Data[key] = ""
	}
	return blanked
}

// Signature hashes the blanked secret so the values never take part in a signature.
func (x *Secret) Signature() (string, error) {
	return helpers.MessageSignature(x.Blanked())
}

func (x *Secret) GetIdentifier() string {
	return x.GetUUID()
}

func (x *Secret) SetIdentifier(identifier string) {
	x.UUID = identifier
}

func (x *Secret) AddMetadata(key, value string) {
	if x.Metadata == nil {
		x.Metadata = make(map[string]string)
	}
	x.Metadata[key] = value
}

func (x *Secret) DeleteMetadata(key string) map[string]string {
	if x.Metadata == nil {
		return nil
	}
	delete(x.Metadata, key)
	return x.Metadata
}

func (x *Secret) SetMetadata(m map[string]string) {
	x.Metadata = m
}
