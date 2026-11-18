package serves

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/seagalia/flock/sdk/contexes"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/observe"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *FlockServer) CreateSecret(ctx context.Context, req *svcsecret.CreateSecretRequest) (*svcsecret.SecretResponse, error) {
	secretInfo := req.GetSecret()
	secretInfo.UUID = uuid.New().String()

	err := checkSecretMetadata(secretInfo)
	if err != nil {
		return nil, err
	}

	sig, err := secretInfo.Signature()
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	err = s.storeMan.Secrets().Create(ctx, secretInfo)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	// the secret is stored: what follows completes the change, it must not stop halfway when the client leaves
	ctx, cancel := detachedContext(ctx)
	defer cancel()

	err = s.addPermForUser(ctx, helpers.TypeUrl[*secret.Secret](), secretInfo.UUID)
	if err != nil {
		return nil, err
	}

	observe.EmitSubject(observer.EventType_CREATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(secretInfo),
		Identifier: secretInfo.UUID,
		Signature:  sig,
	})

	return &svcsecret.SecretResponse{Secret: secretInfo.Blanked()}, nil
}

func (s *FlockServer) ReadSecret(ctx context.Context, req *svccore.UuidRequest) (*svcsecret.SecretResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*secret.Secret](), req.GetUUID(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	secretInfo, err := s.storeMan.Secrets().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	return &svcsecret.SecretResponse{Secret: secretInfo.Blanked()}, nil
}

func (s *FlockServer) RevealSecret(ctx context.Context, req *svccore.UuidRequest) (*svcsecret.SecretResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*secret.Secret](), req.GetUUID(), permission.Role_OWNER)
	if err != nil {
		return nil, err
	}

	secretInfo, err := s.storeMan.Secrets().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	username, _, _ := contexes.RetrieveUserInfo(ctx)
	s.entry.Info().
		Str("username", username).
		Str("secret_uuid", secretInfo.GetUUID()).
		Str("secret_name", secretInfo.GetName()).
		Msg("secret revealed")

	return &svcsecret.SecretResponse{Secret: secretInfo}, nil
}

func (s *FlockServer) UpdateSecret(ctx context.Context, req *svcsecret.UpdateSecretRequest) (*svcsecret.SecretResponse, error) {
	secretInfo := req.GetSecret()

	err := s.checkPermission(ctx, helpers.TypeUrl[*secret.Secret](), req.GetUUID(), permission.Role_WRITER)
	if err != nil {
		return nil, err
	}

	secretInfo.UUID = req.GetUUID()

	err = checkSecretMetadata(secretInfo)
	if err != nil {
		return nil, err
	}

	stored, err := s.storeMan.Secrets().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	err = s.keepStoredValues(ctx, secretInfo, stored)
	if err != nil {
		return nil, err
	}

	err = checkNameChangedByOwner[*secret.Secret](ctx, s.storeMan, secretInfo, func() error {
		return s.checkPermission(ctx, helpers.TypeUrl[*secret.Secret](), req.GetUUID(), permission.Role_OWNER)
	})
	if err != nil {
		return nil, err
	}

	sig, err := secretInfo.Signature()
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	err = s.storeMan.Secrets().Update(ctx, req.GetUUID(), secretInfo)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to update secret: %v", err).WithCause(err)
	}

	observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(secretInfo),
		Identifier: secretInfo.UUID,
		Signature:  sig,
	})

	return &svcsecret.SecretResponse{Secret: secretInfo.Blanked()}, nil
}

func (s *FlockServer) DeleteSecret(ctx context.Context, req *svccore.UuidRequest) (*emptypb.Empty, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*secret.Secret](), req.GetUUID(), permission.Role_OWNER)
	if err != nil {
		return nil, err
	}

	users, err := s.secretUsers(ctx, req.GetUUID())
	if err != nil {
		return nil, err
	}
	if len(users) > 0 {
		return nil, connect.Errorf(connect.CodeFailedPrecondition,
			"secret %s is used by %s .", req.GetUUID(), strings.Join(users, ", "),
		)
	}

	err = s.storeMan.Secrets().Delete(ctx, req.GetUUID())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to delete secret: %v", err).WithCause(err)
	}

	observe.EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrl[*secret.Secret](),
		Identifier: req.GetUUID(),
	})
	return &emptypb.Empty{}, nil
}

func (s *FlockServer) ListSecret(ctx context.Context, req *svcsecret.ListSecretRequest) (*svcsecret.ListSecretResponse, error) {
	secrets, err := s.storeMan.Secrets().List(ctx, s.makeListOptions(ctx, req), req.GetFilters()...)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to list secrets: %v", err).WithCause(err)
	}

	blanked := make([]*secret.Secret, 0, len(secrets))
	for _, secretInfo := range secrets {
		blanked = append(blanked, secretInfo.Blanked())
	}

	return &svcsecret.ListSecretResponse{Secrets: blanked}, nil
}

// checkSecretMetadata refuses a secret that points at another one, because its metadata is never encrypted and a user
// could believe a value is protected when it is not.
func checkSecretMetadata(secretInfo *secret.Secret) error {
	for _, key := range slices.Sorted(maps.Keys(secretInfo.GetMetadata())) {
		if strings.HasPrefix(secretInfo.GetMetadata()[key], secret.RefPrefix) {
			return connect.Errorf(connect.CodeInvalidArgument,
				"metadata key %q of a secret cannot point at a secret", key,
			)
		}
	}
	return nil
}

// keepStoredValues fills the keys of updated sent with an empty value from stored, and refuses the update when it
// leaves a new key with no value or removes a key a resource points at.
func (s *FlockServer) keepStoredValues(ctx context.Context, updated *secret.Secret, stored *secret.Secret) error {
	for _, key := range slices.Sorted(maps.Keys(updated.GetData())) {
		if updated.Data[key] != "" {
			continue
		}
		storedValue, isStored := stored.GetData()[key]
		if !isStored {
			return connect.Errorf(connect.CodeInvalidArgument, "key %q needs a value", key)
		}
		updated.Data[key] = storedValue
	}

	for _, key := range slices.Sorted(maps.Keys(stored.GetData())) {
		_, isKept := updated.GetData()[key]
		if isKept {
			continue
		}
		users, err := s.secretKeyUsers(ctx, stored.GetUUID(), key)
		if err != nil {
			return err
		}
		if len(users) > 0 {
			return connect.Errorf(connect.CodeFailedPrecondition,
				"key %q of secret %s is used by %s .", key, stored.GetUUID(), strings.Join(users, ", "),
			)
		}
	}
	return nil
}

func (s *FlockServer) secretUsers(ctx context.Context, secretUuid string) ([]string, error) {
	return s.secretKeyUsers(ctx, secretUuid, "")
}

func (s *FlockServer) secretKeyUsers(ctx context.Context, secretUuid string, key string) ([]string, error) {
	findInKind := []func() ([]string, error){
		func() ([]string, error) {
			return secretUsersOfKind(ctx, s.storeMan.VirtualServers(), "virtual server", secretUuid, key)
		},
		func() ([]string, error) {
			return secretUsersOfKind(ctx, s.storeMan.Backends(), "backend", secretUuid, key)
		},
		func() ([]string, error) { return secretUsersOfKind(ctx, s.storeMan.VIPs(), "vip", secretUuid, key) },
		func() ([]string, error) {
			return secretUsersOfKind(ctx, s.storeMan.Certificates(), "certificate", secretUuid, key)
		},
		func() ([]string, error) { return secretUsersOfKind(ctx, s.storeMan.Zones(), "zone", secretUuid, key) },
		func() ([]string, error) {
			return secretUsersOfKind(ctx, s.storeMan.Instances(), "instance", secretUuid, key)
		},
	}
	var users []string
	for _, find := range findInKind {
		found, err := find()
		if err != nil {
			return nil, errors.ToConnectError(err)
		}
		users = append(users, found...)
	}
	return users, nil
}

func secretUsersOfKind[T proto.Message](
	ctx context.Context,
	store storers.Storer[T], kind string, secretUuid string, key string,
) ([]string, error) {
	userUuids, err := store.FindSrcWithForeignResource(ctx, helpers.TypeUrl[*secret.Secret](), secretUuid)
	if err != nil {
		return nil, err
	}
	var users []string
	for _, userUuid := range userUuids {
		user, err := store.Read(ctx, userUuid)
		if err != nil {
			return nil, err
		}
		if key != "" && !pointsAtKey(user, secretUuid, key) {
			continue
		}
		users = append(users, fmt.Sprintf("%s %s", kind, helpers.GetName(user)))
	}
	return users, nil
}

func pointsAtKey(resource proto.Message, secretUuid string, key string) bool {
	refs, _ := secret.FindRefs(resource)
	for _, found := range refs {
		if found.Ref.SecretUUID == secretUuid && found.Ref.Key == key {
			return true
		}
	}
	return false
}
