package serves

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

// checkSecretRefs checks the secret references of after, those of its top-level metadata and of its typed fields,
// before being the stored resource (nil on create). Only a reference that is new or moved to another place is checked
// for existence and the reader role, so a writer of a resource can keep updating it without any role on the secrets it
// already points at. Reading is enough to point at a secret, as mounting a secret in a pod is in Kubernetes, though a
// reader can then get the value through a resource pointing at it.
func (s *FlockServer) checkSecretRefs(ctx context.Context, before, after proto.Message) error {
	afterRefs, err := secret.FindRefs(after)
	if err != nil {
		return connect.Errorf(connect.CodeInvalidArgument, "%v", err)
	}
	beforeRefs := make(map[string]secret.Ref)
	if before != nil {
		// the stored resource was checked when it was written, a malformed reference there is only left out
		found, _ := secret.FindRefs(before)
		for _, ref := range found {
			beforeRefs[ref.Path] = ref.Ref
		}
	}

	for _, found := range afterRefs {
		ref := found.Ref
		isUnchanged := beforeRefs[found.Path] == ref
		if isUnchanged {
			continue
		}

		// the role comes first so a caller with no role learns nothing of the secret, not even if it exists
		err := s.checkPermission(ctx, helpers.TypeUrl[*secret.Secret](), ref.SecretUUID, permission.Role_READER)
		if connect.CodeOf(err) == connect.CodePermissionDenied {
			return connect.Errorf(connect.CodePermissionDenied, "%s: pointing at secret %s needs the reader role on it", found.Path, ref.SecretUUID)
		}
		if err != nil {
			return err
		}

		stored, err := s.storeMan.Secrets().Read(ctx, ref.SecretUUID)
		if errors.IsErrNotFound(err) {
			return connect.Errorf(connect.CodeInvalidArgument, "%s: secret %s does not exist", found.Path, ref.SecretUUID)
		}
		if err != nil {
			return errors.ToConnectError(err)
		}
		if _, hasKey := stored.GetData()[ref.Key]; !hasKey {
			return connect.Errorf(connect.CodeInvalidArgument, "%s: secret %s has no key %q", found.Path, ref.SecretUUID, ref.Key)
		}
	}
	return nil
}
