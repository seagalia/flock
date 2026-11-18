package serves

import (
	"context"
	"fmt"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/hashicorp/go-multierror"
	"github.com/samber/lo"
	"github.com/seagalia/flock/sdk/contexes"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	// ReservedUuidPrefix prefixes the resources created at boot, permissions can't be added on them
	ReservedUuidPrefix = "00000000-0000-0000-0000-"
)

func isReservedUuid(uuid string) bool {
	return strings.HasPrefix(uuid, ReservedUuidPrefix)
}

type reqPermModify interface {
	proto.Message
	GetResourceType() string
	GetResourceUuid() string
}

func (s *FlockServer) AddPermission(ctx context.Context, req *svcperm.AddPermissionRequest) (*emptypb.Empty, error) {
	err := s.checkPermModifyReq(ctx, req)
	if err != nil {
		return nil, err
	}

	err = s.addPermReq(ctx, req)
	if err != nil {
		return nil, err
	}

	return &emptypb.Empty{}, nil
}

func (s *FlockServer) addPermReq(ctx context.Context, req *svcperm.AddPermissionRequest) error {
	if isReservedUuid(req.GetResourceUuid()) &&
		// allow to register permission during boot
		!(req.GetRole() == permission.Role_READER &&
			req.GetMember().GetMemberType() == permission.MemberType_USER &&
			req.GetMember().GetMemberName() == "boot") {
		return nil
	}
	s.entry.Debug().
		Str("action", "addPermReq").
		Str("res_type", req.GetResourceType()).
		Str("res_uuid", req.GetResourceUuid()).
		Str("role", req.GetRole().String()).
		Stringer("member_type", req.GetMember().GetMemberType()).
		Str("member_name", req.GetMember().GetMemberName()).
		Msg("adding permission")
	err := s.permissioner.AddPermission(ctx,
		req.GetResourceType(),
		req.GetResourceUuid(),
		req.GetRole(),
		req.GetMember().GetMemberType(),
		req.GetMember().GetMemberName(),
	)
	if err != nil {
		return connect.Errorf(connect.CodeInternal,
			"failed to add permission to %s with identifier %s: %s",
			req.GetResourceType(), req.GetResourceUuid(),
			err.Error(),
		)
	}

	mapForeigns, err := s.mapForeignsResSrc(ctx, req.GetResourceType(), req.GetResourceUuid())
	if err != nil {
		return err
	}
	var result error
	for foreignType, foreignIds := range mapForeigns {
		for _, foreignId := range foreignIds {
			// a resource may reference what its user can only read: the role reaches the referenced resources the caller
			// owns, otherwise referencing a backend would be enough to get any role on it
			owned, err := s.callerOwns(ctx, foreignType, foreignId)
			if err != nil {
				result = multierror.Append(result, err)
				continue
			}
			if !owned {
				continue
			}
			addReq := &svcperm.AddPermissionRequest{
				ResourceType: foreignType,
				ResourceUuid: foreignId,
				Role:         req.GetRole(),
				Member:       req.GetMember(),
			}
			err = s.addPermReq(ctx, addReq)
			if err != nil {
				result = multierror.Append(result, err)
				continue
			}
		}
	}
	return result
}

func (s *FlockServer) SetAllowAllRead(ctx context.Context, req *svcperm.SetAllowAllReadRequest) (*emptypb.Empty, error) {
	err := s.checkPermModifyReq(ctx, req)
	if err != nil {
		return nil, err
	}

	err = s.permissioner.SetAllowAllRead(ctx, req.GetResourceType(), req.GetResourceUuid(), req.GetAllowAllRead())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to set allow all read: %v", err).WithCause(err)
	}
	return &emptypb.Empty{}, nil
}

func (s *FlockServer) HasPermission(ctx context.Context, req *svcperm.HasPermissionRequest) (*svcperm.HasPermissionResponse, error) {
	err := s.checkPermission(ctx, req.GetResourceType(), req.GetResourceUuid(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	username := ""
	groups := make([]string, 0)
	if req.GetMember().GetMemberType() == permission.MemberType_GROUP {
		groups = []string{req.GetMember().GetMemberName()}
	}
	if req.GetMember().GetMemberType() == permission.MemberType_USER {
		username = req.GetMember().GetMemberName()
	}
	ok, err := s.permissioner.HasPermission(ctx,
		req.GetResourceType(), req.GetResourceUuid(),
		req.GetRole(),
		username, groups,
	)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to check permission: %v", err).WithCause(err)
	}
	return &svcperm.HasPermissionResponse{HasPermission: ok}, nil
}

func (s *FlockServer) DeletePermission(ctx context.Context, req *svcperm.DeletePermissionRequest) (*emptypb.Empty, error) {
	err := s.checkPermModifyReq(ctx, req)
	if err != nil {
		return nil, err
	}
	err = s.delPermReq(ctx, req, false)
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

func (s *FlockServer) mapForeignsResSrc(ctx context.Context, resType string, resId string) (map[string][]string, error) {
	var mapForeigns map[string][]string
	var err error
	switch resType {
	case helpers.TypeUrl[*backend.Backend]():
		mapForeigns, err = s.storeMan.Backends().FindForeignBySrc(ctx, resId)
	case helpers.TypeUrl[*instance.Instance]():
		mapForeigns, err = s.storeMan.Instances().FindForeignBySrc(ctx, resId)
	case helpers.TypeUrl[*vip.VIP]():
		mapForeigns, err = s.storeMan.VIPs().FindForeignBySrc(ctx, resId)
	case helpers.TypeUrl[*vs.VirtualServer]():
		mapForeigns, err = s.storeMan.VirtualServers().FindForeignBySrc(ctx, resId)
	case helpers.TypeUrl[*cert.Certificate]():
		mapForeigns, err = s.storeMan.Certificates().FindForeignBySrc(ctx, resId)
	case helpers.TypeUrl[*secret.Secret]():
		return map[string][]string{}, nil
	default:
		return mapForeigns, fmt.Errorf("no store found for type %s", resType)
	}
	if err != nil {
		return mapForeigns, connect.Errorf(connect.CodeInternal,
			"failed to add permission to %s with identifier %s: %s",
			resType, resId,
			err.Error(),
		)
	}
	// a zone is shared by the instances of every user: the permissions on an instance never reach it
	delete(mapForeigns, helpers.TypeUrl[*zone.Zone]())
	// a secret is shared by every resource that points at it, so a role on such a resource never reaches the secret and
	// its owner decides who can use it
	delete(mapForeigns, helpers.TypeUrl[*secret.Secret]())
	return mapForeigns, nil
}

func (s *FlockServer) delPermReq(ctx context.Context, req *svcperm.DeletePermissionRequest, noCheck bool) error {
	if isReservedUuid(req.GetResourceUuid()) {
		return nil
	}
	perms, err := s.permissioner.ListMemberPermissions(ctx, req.GetResourceType(), req.GetResourceUuid(), permission.Role_OWNER)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "failed to list member permissions: %v", err).WithCause(err)
	}

	if len(perms) == 1 &&
		perms[0].GetMember().GetMemberType() == req.GetMember().GetMemberType() &&
		perms[0].GetMember().GetMemberName() == req.GetMember().GetMemberName() {
		return connect.Errorf(connect.CodeFailedPrecondition, "cannot delete last owner")
	}
	err = s.permissioner.DeletePermission(ctx,
		req.GetResourceType(), req.GetResourceUuid(),
		req.GetMember().GetMemberType(), req.GetMember().GetMemberName(),
	)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "failed to delete permission: %v", err).WithCause(err)
	}

	mapForeigns, err := s.mapForeignsResSrc(ctx, req.GetResourceType(), req.GetResourceUuid())
	if err != nil {
		return err
	}

	var result error
	for foreignType, foreignIds := range mapForeigns {
		for _, foreignId := range foreignIds {
			delReq := &svcperm.DeletePermissionRequest{
				ResourceType: foreignType,
				ResourceUuid: foreignId,
				Member:       req.GetMember(),
			}
			err = s.checkPermModifyReq(ctx, delReq)
			if err != nil {
				continue
			}
			if !noCheck {
				err = s.delPermReq(ctx, delReq, noCheck)
				if err != nil {
					result = multierror.Append(result, err)
					continue
				}
			}

		}
	}
	return result
}

func (s *FlockServer) ListMemberPermissions(ctx context.Context, req *svcperm.ListMemberPermissionsRequest) (*svcperm.ListMemberPermissionsResponse, error) {
	err := s.checkPermission(ctx, req.GetResourceType(), req.GetResourceUuid(), permission.Role_READER)
	if err != nil {
		return nil, err
	}
	role := req.GetRole()
	if req.GetSeeAll() {
		role = -1
	}
	perms, err := s.permissioner.ListMemberPermissions(ctx,
		req.GetResourceType(),
		req.GetResourceUuid(),
		role,
	)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to list member permissions: %v", err).WithCause(err)
	}

	allowAllRead, err := s.permissioner.IsAllowAllRead(ctx, req.GetResourceType(), req.GetResourceUuid())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to read the allow all read flag: %v", err).WithCause(err)
	}

	return &svcperm.ListMemberPermissionsResponse{
		MemberPermissions: perms,
		AllowAllRead:      allowAllRead,
	}, nil
}

func (s *FlockServer) checkPermModifyReq(ctx context.Context, req reqPermModify) error {
	err := s.checkPermission(ctx, req.GetResourceType(), req.GetResourceUuid(), permission.Role_OWNER)
	if err != nil {
		return err
	}
	return nil
}

func (s *FlockServer) checkPermission(ctx context.Context, resType string, resId string, role permission.Role) error {
	username, groups, _ := contexes.RetrieveUserInfo(ctx)
	clientIp := clientAddr(ctx)
	entry := s.entry.With().
		Str("method", "checkPermission").
		Str("res_type", resType).
		Str("res_id", resId).
		Str("username", username).
		Str("role", role.String()).
		Logger()
	if clientIp != "" {
		entry = entry.With().Str("client_ip", clientIp).Logger()
	}

	if s.userIsAdmin(ctx, username, groups) {
		entry.Info().Msg("check permission: Allowed as admin")
		return nil
	}

	ok, err := s.permissioner.HasPermission(ctx, resType, resId, role, username, groups)
	if ctx.Err() != nil {
		return errors.ToConnectError(ctx.Err())
	}
	if err != nil {
		entry.Error().Msgf("check permission failed for user due to permissionner error: %v", err)
		return connect.Errorf(connect.CodeInternal, "check permission failed due to internal error")
	}
	if ok {
		entry.Info().Msg("check permission: Allowed")
		return nil
	}
	entry.Warn().Msg("check permission: Denied")
	return connect.Errorf(connect.CodePermissionDenied, "Your user has no permission %s to do this operation", role.String())
}

// callerOwns checks like checkPermission without its denied log: not owning a referenced resource is a normal case of
// the spread
func (s *FlockServer) callerOwns(ctx context.Context, resType string, resId string) (bool, error) {
	username, groups, _ := contexes.RetrieveUserInfo(ctx)
	if s.userIsAdmin(ctx, username, groups) {
		return true, nil
	}
	ok, err := s.permissioner.HasPermission(ctx, resType, resId, permission.Role_OWNER, username, groups)
	if err != nil {
		return false, fmt.Errorf("failed to check owner of %s with identifier %s: %w", resType, resId, err)
	}
	return ok, nil
}

// userIsAdmin trusts the break-glass marker of ctx and not the username: names built from client headers, like the
// ones of agents, could otherwise spell the reserved username
func (s *FlockServer) userIsAdmin(ctx context.Context, username string, groups []string) bool {
	if contexes.IsBreakGlass(ctx) {
		return true
	}
	return lo.Contains[string](s.admConf.Users, username) || lo.Some[string](s.admConf.Groups, groups)
}

func (s *FlockServer) checkIsAdmin(ctx context.Context) error {
	username, groups, _ := contexes.RetrieveUserInfo(ctx)
	if s.userIsAdmin(ctx, username, groups) {
		return nil
	}
	return connect.Errorf(connect.CodePermissionDenied, "Your user has no admin permission to do this operation")
}

func (s *FlockServer) addPermForUser(ctx context.Context, resType string, resId string) error {
	if s.checkIsAdmin(ctx) == nil {
		return nil
	}
	username, _, _ := contexes.RetrieveUserInfo(ctx)
	err := s.permissioner.AddPermission(ctx, resType, resId, permission.Role_OWNER, permission.MemberType_USER, username)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "failed to add permission for user: %v", err).WithCause(err)
	}
	s.spreadPermToMembers(ctx, resType, resId)
	return nil
}

func (s *FlockServer) spreadPermToMembersRoles(ctx context.Context, resType string, resId string, role permission.Role) {
	entry := s.entry.With().
		Str("action", "spreadPermToMembersRoles").
		Str("role", role.String()).
		Str("res_type", resType).
		Str("res_id", resId).
		Logger()
	// the spread may take many queries (every member, every referenced resource), the call answers without it. It
	// completes a change already stored, so it doesn't stop when the client leaves.
	ctx, cancel := detachedContext(ctx)
	s.background.Go(func() {
		defer cancel()
		entry.Debug().Msg("spreading permission")
		defer entry.Debug().Msg("spreading permission done")
		members, err := s.permissioner.ListMemberPermissions(ctx, resType, resId, role)
		if err != nil {
			entry.Warn().Err(err).Msg("failed to list member permissions")
			return
		}
		var result error
		for _, member := range members {
			addReq := &svcperm.AddPermissionRequest{
				ResourceType: resType,
				ResourceUuid: resId,
				Role:         role,
				Member:       member.GetMember(),
			}
			err := s.addPermReq(ctx, addReq)
			if err != nil {
				result = multierror.Append(result,
					fmt.Errorf("failed to spread permission to %s: %s", member.GetMember().String(), err.Error()),
				)
			}
		}
		if result != nil {
			entry.Warn().Msg(result.Error())
		}
	})
}

// WaitBackground returns once the work the calls started in the background has ended. The calls must have stopped
// first, a sync.WaitGroup can't take new work while it is waited.
func (s *FlockServer) WaitBackground() {
	s.background.Wait()
}

func (s *FlockServer) spreadPermToMembers(ctx context.Context, resType string, resId string) {
	s.spreadPermToMembersRoles(ctx, resType, resId, permission.Role_OWNER)
	s.spreadPermToMembersRoles(ctx, resType, resId, permission.Role_WRITER)
	s.spreadPermToMembersRoles(ctx, resType, resId, permission.Role_READER)
}
