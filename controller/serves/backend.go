package serves

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/observe"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *FlockServer) CreateBackend(ctx context.Context, req *svcbackend.CreateBackendRequest) (*svcbackend.BackendResponse, error) {
	backendInfo := req.GetBackend()
	backendInfo.UUID = uuid.New().String()

	sig, err := helpers.MessageSignature(backendInfo)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	err = s.checkSecretRefs(ctx, nil, backendInfo)
	if err != nil {
		return nil, err
	}

	err = s.storeMan.Backends().Create(ctx, backendInfo)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	// the backend is stored: what follows completes the change, it must not stop halfway when the client leaves
	ctx, cancel := detachedContext(ctx)
	defer cancel()

	err = s.addPermForUser(ctx, helpers.TypeUrl[*backend.Backend](), backendInfo.UUID)
	if err != nil {
		return nil, err
	}

	observe.EmitSubject(observer.EventType_CREATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(backendInfo),
		Identifier: backendInfo.UUID,
		Signature:  sig,
	})

	return &svcbackend.BackendResponse{
		Backend: backendInfo,
	}, nil
}

func (s *FlockServer) ReadBackend(ctx context.Context, req *svccore.UuidRequest) (*svcbackend.BackendResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*backend.Backend](), req.GetUUID(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	backendInfo, err := s.storeMan.Backends().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	return &svcbackend.BackendResponse{
		Backend: backendInfo,
	}, nil

}
func (s *FlockServer) UpdateBackend(ctx context.Context, req *svcbackend.UpdateBackendRequest) (*svcbackend.BackendResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*backend.Backend](), req.GetUUID(), permission.Role_WRITER)
	if err != nil {
		return nil, err
	}

	backendInfo := req.GetBackend()
	backendInfo.UUID = req.GetUUID()

	err = checkNameChangedByOwner[*backend.Backend](ctx, s.storeMan, backendInfo, func() error {
		return s.checkPermission(ctx, helpers.TypeUrl[*backend.Backend](), req.GetUUID(), permission.Role_OWNER)
	})
	if err != nil {
		return nil, err
	}

	storedBackend, err := s.storeMan.Backends().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	err = s.checkSecretRefs(ctx, storedBackend, backendInfo)
	if err != nil {
		return nil, err
	}

	sig, err := helpers.MessageSignature(backendInfo)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	err = s.storeMan.Backends().Update(ctx, backendInfo.UUID, backendInfo)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	s.spreadPermToMembers(ctx, helpers.TypeUrl[*backend.Backend](), backendInfo.UUID)

	observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(backendInfo),
		Identifier: backendInfo.UUID,
		Signature:  sig,
	})

	return &svcbackend.BackendResponse{
		Backend: backendInfo,
	}, nil
}

func (s *FlockServer) DeleteBackend(ctx context.Context, req *svccore.UuidRequest) (*emptypb.Empty, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*backend.Backend](), req.GetUUID(), permission.Role_OWNER)
	if err != nil {
		return nil, err
	}

	foundVss, err := s.storeMan.VirtualServers().FindSrcWithForeignResource(ctx, helpers.TypeUrl[*backend.Backend](), req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	if len(foundVss) > 0 {
		return nil, connect.Errorf(connect.CodeFailedPrecondition,
			"backend %s is used by virtual server(s) %s .", req.GetUUID(), strings.Join(foundVss, ", "),
		)
	}

	err = s.storeMan.Backends().Delete(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	observe.EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrl[*backend.Backend](),
		Identifier: req.GetUUID(),
	})

	return &emptypb.Empty{}, nil
}

func (s *FlockServer) ListBackend(ctx context.Context, req *svcbackend.ListBackendRequest) (*svcbackend.ListBackendResponse, error) {
	backendList, err := s.storeMan.Backends().List(ctx, s.makeListOptions(ctx, req), req.GetFilters()...)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	return &svcbackend.ListBackendResponse{
		Backends: backendList,
	}, nil
}
