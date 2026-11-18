package serves

import (
	"context"
	stderrors "errors"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/controller/lockers"
	"github.com/seagalia/flock/sdk/agentsdk/leases"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	svclease "github.com/seagalia/flock/sdk/flock/services/lease/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *FlockServer) Acquire(ctx context.Context, req *svclease.AcquireRequest) (*svclease.AcquireResponse, error) {
	err := s.checkPermission(ctx, req.GetTypeUrl(), req.GetUUID(), permission.Role_WRITER)
	if err != nil {
		return nil, err
	}
	declared, err := s.identifyAgent(ctx, s.entry, req.GetInstanceUuid())
	if err != nil {
		return nil, err
	}

	subject := req.GetTypeUrl() + "/" + req.GetUUID()
	holder := lockers.LeaseHolder{Uuid: declared.GetUUID(), Name: declared.GetName()}
	lease, err := s.leases.Acquire(ctx, subject, holder, clampLeaseTTL(req.GetTtl()))
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	return &svclease.AcquireResponse{
		Granted:    lease.Granted,
		LeaseId:    lease.Id,
		HolderUuid: lease.Holder.Uuid,
		HolderName: lease.Holder.Name,
		ExpiresAt:  timestamppb.New(lease.ExpiresAt),
	}, nil
}

func (s *FlockServer) Renew(ctx context.Context, req *svclease.RenewRequest) (*svclease.RenewResponse, error) {
	commonName, err := leaseCallerName(ctx)
	if err != nil {
		return nil, err
	}
	expiresAt, err := s.leases.Renew(ctx, req.GetLeaseId(), commonName, clampLeaseTTL(req.GetTtl()))
	if stderrors.Is(err, lockers.ErrLeaseLost) {
		return nil, connect.Errorf(connect.CodeNotFound, "the lease %s is lost, it expired or is held by another agent",
			req.GetLeaseId()).WithCause(err)
	}
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	return &svclease.RenewResponse{ExpiresAt: timestamppb.New(expiresAt)}, nil
}

func (s *FlockServer) Release(ctx context.Context, req *svclease.ReleaseRequest) (*emptypb.Empty, error) {
	commonName, err := leaseCallerName(ctx)
	if err != nil {
		return nil, err
	}
	err = s.leases.Release(ctx, req.GetLeaseId(), commonName)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	return &emptypb.Empty{}, nil
}

// leaseCallerName is the holder name of a lease: the common name of the client certificate, which Acquire matched to a
// declared agent when it granted the lease.
func leaseCallerName(ctx context.Context) (string, error) {
	commonName, hasCertificate := auth.ClientCommonName(ctx)
	if !hasCertificate {
		return "", connect.Errorf(connect.CodePermissionDenied, "a client certificate is required to hold a lease")
	}
	return commonName, nil
}

func clampLeaseTTL(ttl *durationpb.Duration) time.Duration {
	if ttl == nil {
		return leases.DefaultTTL
	}
	return min(max(ttl.AsDuration(), leases.MinTTL), leases.MaxTTL)
}
