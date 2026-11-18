package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

// secretNamePrefix marks a reference that names its secret instead of giving its uuid.
const secretNamePrefix = secret.RefPrefix + "name:"

// keyRefNamePrefix marks the secret_uuid of a typed reference that names its secret.
const keyRefNamePrefix = "name:"

// resolveSecretRefs replaces in place the secret names of m by their uuids: each `secret://name:<secret name>/<key>`
// value of its top-level metadata becomes `secret://<secret uuid>/<key>`, and each `secret_uuid: name:<secret name>` of
// its typed references becomes the uuid. The controller checks the references, so uuids and plain values are left
// alone.
func resolveSecretRefs(ctx context.Context, client *clients.Client, m proto.Message) error {
	names := secretNames{client: client, uuids: make(map[string]string)}

	metadata := helpers.GetMetadata(m)
	for _, key := range slices.Sorted(maps.Keys(metadata)) {
		rest, isNamed := strings.CutPrefix(metadata[key], secretNamePrefix)
		if !isNamed {
			continue
		}
		// The key never holds a slash, but a name may.
		slash := strings.LastIndex(rest, "/")
		if slash <= 0 || slash == len(rest)-1 {
			return fmt.Errorf("metadata %q: %q is not a reference of the form %s<secret name>/<key>",
				key, metadata[key], secretNamePrefix)
		}
		name, secretKey := rest[:slash], rest[slash+1:]

		secretUUID, err := names.uuidOf(ctx, name, fmt.Sprintf("%s<secret uuid>/%s", secret.RefPrefix, secretKey))
		if err != nil {
			return fmt.Errorf("metadata %q: %w", key, err)
		}
		metadata[key] = secret.Ref{SecretUUID: secretUUID, Key: secretKey}.String()
	}

	return secret.EachKeyRef(m, func(path string, keyRef *flocktype.SecretKeyRef) error {
		name, isNamed := strings.CutPrefix(keyRef.GetSecretUuid(), keyRefNamePrefix)
		if !isNamed {
			return nil
		}
		secretUUID, err := names.uuidOf(ctx, name, "the secret uuid in secret_uuid")
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		keyRef.SecretUuid = secretUUID
		return nil
	})
}

// secretNames finds secrets by name, each name once per resource.
type secretNames struct {
	client *clients.Client
	uuids  map[string]string
}

// uuidOf gives the uuid of the secret called name, insteadHint telling what to write when several secrets share it.
func (n secretNames) uuidOf(ctx context.Context, name string, insteadHint string) (string, error) {
	if secretUUID, isKnown := n.uuids[name]; isKnown {
		return secretUUID, nil
	}
	found, err := FindTypedByName(ctx, n.client, helpers.TypeUrl[*secret.Secret](), name)
	if err != nil {
		return "", err
	}
	if len(found) == 0 {
		return "", fmt.Errorf("no secret named %q", name)
	}
	// A rename can give two secrets the same name, and picking one would point at a credential by chance.
	if len(found) > 1 {
		return "", fmt.Errorf("%d secrets are named %q, use %s", len(found), name, insteadHint)
	}
	secretUUID := found[0].(*secret.Secret).GetUUID()
	n.uuids[name] = secretUUID
	return secretUUID, nil
}
