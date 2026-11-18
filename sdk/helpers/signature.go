package helpers

import (
	"fmt"

	"github.com/cespare/xxhash/v2"
	"google.golang.org/protobuf/proto"
)

func MessageSignature(m proto.Message) (string, error) {
	// deterministic marshalling is only stable within go, fine as only go makes signatures, and a schema change
	// changes the signature as wanted. No UseCachedSize: a nested message mutated after a previous marshal keeps a
	// stale cached size, which protobuf >= 1.34 rejects with a size mismatch error.
	b, err := proto.MarshalOptions{
		AllowPartial:  true,
		Deterministic: true,
	}.Marshal(m)
	if err != nil {
		return "", err
	}
	h := xxhash.New()
	h.Write(b)
	return fmt.Sprint(h.Sum64()), nil
}
