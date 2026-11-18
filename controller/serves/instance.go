package serves

import (
	"context"
	stderrors "errors"
	"net/netip"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/samber/lo"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/controller/ipams"
	"github.com/seagalia/flock/controller/lockers"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/observe"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *FlockServer) CreateInstance(ctx context.Context, req *svcinstance.CreateInstanceRequest) (*svcinstance.InstanceResponse, error) {
	instanceInfo := req.GetInstance()
	instanceInfo.UUID = uuid.New().String()

	sig, err := helpers.MessageSignature(instanceInfo)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	// an instance announces its vips: like add-vip-to-instance, the user must be able to read them
	for _, vipUuid := range instanceInfo.GetVipsUuid() {
		err = s.checkPermission(ctx, helpers.TypeUrl[*vip.VIP](), vipUuid, permission.Role_READER)
		if err != nil {
			return nil, err
		}
	}

	err = s.checkSecretRefs(ctx, nil, instanceInfo)
	if err != nil {
		return nil, err
	}

	if err := s.locker.Lock(ctx, lockers.InstanceVips); err != nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "could not lock %s: %v", lockers.InstanceVips, err).WithCause(err)
	}
	defer s.locker.Unlock(ctx, lockers.InstanceVips)

	zoneInfo, err := s.checkInstanceRequest(ctx, instanceInfo)
	if err != nil {
		return nil, err
	}

	err = s.acquireVipsIps(ctx, zoneInfo, instanceInfo.GetVipsUuid())
	if err != nil {
		return nil, err
	}

	// the ips are taken: what follows completes the change, it must not stop halfway when the client leaves
	ctx, cancel := detachedContext(ctx)
	defer cancel()

	originating := strings.Join(requestHeaderValues(ctx, "originating"), ",")

	err = s.instanceStatusToErr(s.instantiater.CreateServiceInstance(ctx, instantiaters.InstanceParams{
		InstanceUUID:        instanceInfo.GetUUID(),
		Zone:                zoneInfo.GetName(),
		ResourceTypeName:    instanceInfo.GetResourceType(),
		OriginatingIdentity: originating,
	}))
	if err != nil {
		s.releaseVipsIps(ctx, zoneInfo, instanceInfo.GetVipsUuid())
		return nil, err
	}

	err = s.storeMan.Instances().Create(ctx, instanceInfo)
	if err != nil {
		s.releaseVipsIps(ctx, zoneInfo, instanceInfo.GetVipsUuid())
		return nil, errors.ToConnectError(err)
	}

	err = s.addPermForUser(ctx, helpers.TypeUrl[*instance.Instance](), instanceInfo.UUID)
	if err != nil {
		return nil, err
	}

	observe.EmitSubject(observer.EventType_CREATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(instanceInfo),
		Identifier: instanceInfo.UUID,
		Signature:  sig,
	})

	return &svcinstance.InstanceResponse{
		Instance: instanceInfo,
	}, nil
}

func (s *FlockServer) instanceStatusToErr(instStatus *instance.InstanceStatus, err error) error {
	if err != nil {
		return err
	}
	if instStatus == nil {
		return nil
	}
	if instStatus.GetCurrentState() != instance.InstanceStatus_ERRORED {
		return nil
	}
	return connect.Errorf(connect.CodeInternal, "service instance is in error state: %s", instStatus.GetErrorMessage())
}

// checkInstanceRequest gives the zone of the instance.
func (s *FlockServer) checkInstanceRequest(ctx context.Context, instanceInfo *instance.Instance) (*zone.Zone, error) {
	zoneInfo, err := s.storeMan.Zones().Read(ctx, instanceInfo.GetZoneUuid())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	can, err := s.instantiater.CanInstantiate(ctx, instantiaters.InstanceParams{
		InstanceUUID:     instanceInfo.GetUUID(),
		Zone:             zoneInfo.GetName(),
		ResourceTypeName: instanceInfo.GetResourceType(),
	})
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not check if instance can be instantiated: %v", err).WithCause(err)
	}
	if !can {
		return nil, connect.Errorf(connect.CodeFailedPrecondition,
			"instance cannot be instantiated in zone %s with resource type %s", zoneInfo.GetName(), instanceInfo.GetResourceType(),
		)
	}
	for _, vipUuid := range instanceInfo.GetVipsUuid() {
		exists, err := s.storeMan.VIPs().Exists(ctx, vipUuid)
		if err != nil {
			return nil, connect.Errorf(connect.CodeInternal, "could not find vip: %v", err).WithCause(err)
		}
		if !exists {
			return nil, connect.Errorf(connect.CodeNotFound, "vip %s does not exist", vipUuid)
		}
		err = s.checkVipFreeInZone(ctx, vipUuid, instanceInfo)
		if err != nil {
			return nil, err
		}
	}

	return zoneInfo, nil
}

// checkVipFreeInZone refuses a vip already on another instance of the zone of instanceInfo: a vip can be
// on several instances, each in its own zone (same ip on networks of different places).
func (s *FlockServer) checkVipFreeInZone(ctx context.Context, vipUuid string, instanceInfo *instance.Instance) error {
	founds, err := s.storeMan.Instances().FindSrcWithForeignResource(ctx, helpers.TypeUrl[*vip.VIP](), vipUuid)
	if err != nil {
		return errors.ToConnectError(err)
	}
	for _, found := range founds {
		if found == instanceInfo.GetUUID() {
			continue
		}
		other, err := s.storeMan.Instances().Read(ctx, found)
		if err != nil {
			return errors.ToConnectError(err)
		}
		if other.GetZoneUuid() == instanceInfo.GetZoneUuid() {
			return connect.Errorf(connect.CodeAlreadyExists,
				"vip %s is already used by instance %s in zone %s", vipUuid, found, instanceInfo.GetZoneUuid(),
			)
		}
	}
	return nil
}

func (s *FlockServer) ReadInstance(ctx context.Context, req *svccore.UuidRequest) (*svcinstance.InstanceResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetUUID(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	instanceInfo, err := s.storeMan.Instances().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not read instance: %v", err).WithCause(err)
	}

	return &svcinstance.InstanceResponse{
		Instance: instanceInfo,
	}, nil
}

func (s *FlockServer) UpdateInstance(ctx context.Context, req *svcinstance.UpdateInstanceRequest) (*svcinstance.InstanceResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetUUID(), permission.Role_WRITER)
	if err != nil {
		return nil, err
	}

	instanceInfo := req.GetInstance()
	instanceInfo.UUID = req.GetUUID()

	if err := s.locker.Lock(ctx, lockers.InstanceVips); err != nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "could not lock %s: %v", lockers.InstanceVips, err).WithCause(err)
	}
	defer s.locker.Unlock(ctx, lockers.InstanceVips)

	oldInstanceInfo, err := s.storeMan.Instances().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not read instance: %v", err).WithCause(err)
	}

	if oldInstanceInfo.GetZoneUuid() != instanceInfo.GetZoneUuid() {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "cannot update zone of an instance")
	}

	if oldInstanceInfo.GetResourceType() != instanceInfo.GetResourceType() {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "cannot update resource type of an instance")
	}

	// the vips of an instance hold ips in the ipam of its zone: they change with add and delete vip only
	if !lo.ElementsMatch(oldInstanceInfo.GetVipsUuid(), instanceInfo.GetVipsUuid()) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "cannot update vips of an instance, use add and delete vips.")
	}

	zoneInfo, err := s.checkInstanceRequest(ctx, instanceInfo)
	if err != nil {
		return nil, err
	}

	err = checkNameChangedByOwner[*instance.Instance](ctx, s.storeMan, instanceInfo, func() error {
		return s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetUUID(), permission.Role_OWNER)
	})
	if err != nil {
		return nil, err
	}

	err = s.checkSecretRefs(ctx, oldInstanceInfo, instanceInfo)
	if err != nil {
		return nil, err
	}

	originating := strings.Join(requestHeaderValues(ctx, "originating"), ",")

	err = s.instanceStatusToErr(s.instantiater.UpdateServiceInstance(ctx, instantiaters.InstanceParams{
		InstanceUUID:        instanceInfo.GetUUID(),
		Zone:                zoneInfo.GetName(),
		ResourceTypeName:    instanceInfo.GetResourceType(),
		OriginatingIdentity: originating,
	}))
	if err != nil {
		return nil, err
	}

	err = s.saveInstance(ctx, instanceInfo)
	if err != nil {
		return nil, err
	}

	return &svcinstance.InstanceResponse{
		Instance: instanceInfo,
	}, nil
}

func (s *FlockServer) saveInstance(ctx context.Context, instanceInfo *instance.Instance) error {
	sig, err := helpers.MessageSignature(instanceInfo)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	err = s.storeMan.Instances().Update(ctx, instanceInfo.GetUUID(), instanceInfo)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "could not update instance: %v", err).WithCause(err)
	}

	s.spreadPermToMembers(ctx, helpers.TypeUrl[*instance.Instance](), instanceInfo.GetUUID())

	observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(instanceInfo),
		Identifier: instanceInfo.GetUUID(),
		Signature:  sig,
	})
	return nil
}

func (s *FlockServer) DeleteInstance(ctx context.Context, req *svccore.UuidRequest) (*emptypb.Empty, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetUUID(), permission.Role_OWNER)
	if err != nil {
		return nil, err
	}

	if err := s.locker.Lock(ctx, lockers.InstanceVips); err != nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "could not lock %s: %v", lockers.InstanceVips, err).WithCause(err)
	}
	defer s.locker.Unlock(ctx, lockers.InstanceVips)

	instanceInfo, err := s.storeMan.Instances().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	err = s.instanceStatusToErr(s.instantiater.DeleteServiceInstance(ctx, req.GetUUID()))
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return nil, err
	}
	// the instance is gone from the instantiater: what follows completes the delete, a retry would answer NotFound
	// and leave the ips of its vips allocated
	ctx, cancel := detachedContext(ctx)
	defer cancel()

	err = s.storeMan.Instances().Delete(ctx, req.GetUUID())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not delete instance: %v", err).WithCause(err)
	}

	err = s.storeMan.Nodes().DeleteNodes(ctx, req.GetUUID())
	if err != nil {
		s.entry.Error().Err(err).Str("instance_uuid", req.GetUUID()).Msg("could not delete the nodes of the instance")
	}

	zoneInfo, err := s.storeMan.Zones().Read(ctx, instanceInfo.GetZoneUuid())
	if err != nil {
		s.entry.Error().Err(err).Str("instance_uuid", req.GetUUID()).Msg("could not read zone to release the ips of the vips")
	} else {
		s.releaseVipsIps(ctx, zoneInfo, instanceInfo.GetVipsUuid())
	}

	observe.EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrl[*instance.Instance](),
		Identifier: req.GetUUID(),
	})
	return &emptypb.Empty{}, nil
}

func (s *FlockServer) ListInstance(ctx context.Context, req *svcinstance.ListInstanceRequest) (*svcinstance.ListInstanceResponse, error) {
	instances, err := s.storeMan.Instances().List(ctx, s.makeListOptions(ctx, req), req.GetFilters()...)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not list instances: %v", err).WithCause(err)
	}

	return &svcinstance.ListInstanceResponse{
		Instances: instances,
	}, nil
}

func (s *FlockServer) GetInstanceStatus(ctx context.Context, req *svccore.UuidRequest) (*svcinstance.GetInstanceStatusResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetUUID(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	instStatus, err := s.instantiater.GetStatus(ctx, req.GetUUID())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not get instance status: %v", err).WithCause(err)
	}

	return &svcinstance.GetInstanceStatusResponse{
		Status: instStatus,
	}, nil
}

func (s *FlockServer) AddVipToInstance(ctx context.Context, req *svcinstance.AddVipToInstanceRequest) (*emptypb.Empty, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetUUID(), permission.Role_WRITER)
	if err != nil {
		return nil, err
	}
	err = s.checkPermission(ctx, helpers.TypeUrl[*vip.VIP](), req.GetVipUuid(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	if err := s.locker.Lock(ctx, lockers.InstanceVips); err != nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "could not lock %s: %v", lockers.InstanceVips, err).WithCause(err)
	}
	defer s.locker.Unlock(ctx, lockers.InstanceVips)

	err = s.attachVip(ctx, req.GetUUID(), req.GetVipUuid())
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// attachVip expects lockers.InstanceVips to be held.
func (s *FlockServer) attachVip(ctx context.Context, instanceUuid, vipUuid string) error {
	instanceInfo, err := s.storeMan.Instances().Read(ctx, instanceUuid)
	if err != nil {
		return errors.ToConnectError(err)
	}
	if lo.Contains(instanceInfo.GetVipsUuid(), vipUuid) {
		return nil
	}

	exists, err := s.storeMan.VIPs().Exists(ctx, vipUuid)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "could not find vip: %v", err).WithCause(err)
	}
	if !exists {
		return connect.Errorf(connect.CodeNotFound, "vip %s does not exist", vipUuid)
	}

	err = s.checkVipFreeInZone(ctx, vipUuid, instanceInfo)
	if err != nil {
		return err
	}

	zoneInfo, err := s.storeMan.Zones().Read(ctx, instanceInfo.GetZoneUuid())
	if err != nil {
		return errors.ToConnectError(err)
	}
	err = s.acquireVipIp(ctx, zoneInfo, vipUuid)
	if err != nil {
		return err
	}

	instanceInfo.VipsUuid = append(instanceInfo.GetVipsUuid(), vipUuid)
	err = s.saveInstance(ctx, instanceInfo)
	if err != nil {
		s.releaseVipsIps(ctx, zoneInfo, []string{vipUuid})
		return err
	}
	return nil
}

func (s *FlockServer) DeleteVipFromInstance(ctx context.Context, req *svcinstance.DeleteVipFromInstanceRequest) (*emptypb.Empty, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetUUID(), permission.Role_WRITER)
	if err != nil {
		return nil, err
	}

	if err := s.locker.Lock(ctx, lockers.InstanceVips); err != nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "could not lock %s: %v", lockers.InstanceVips, err).WithCause(err)
	}
	defer s.locker.Unlock(ctx, lockers.InstanceVips)

	instanceInfo, err := s.storeMan.Instances().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	index := lo.IndexOf(instanceInfo.GetVipsUuid(), req.GetVipUuid())
	if index < 0 {
		return &emptypb.Empty{}, nil
	}
	instanceInfo.VipsUuid = lo.DropByIndex(instanceInfo.GetVipsUuid(), index)

	// released first: a failed release keeps the vip on the instance, a retry can release it again
	zoneInfo, err := s.storeMan.Zones().Read(ctx, instanceInfo.GetZoneUuid())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	err = s.releaseVipIp(ctx, zoneInfo, req.GetVipUuid())
	if err != nil {
		return nil, err
	}

	err = s.saveInstance(ctx, instanceInfo)
	if err != nil {
		undoCtx, cancel := detachedContext(ctx)
		defer cancel()
		reacquireErr := s.acquireVipIp(undoCtx, zoneInfo, req.GetVipUuid())
		if reacquireErr != nil {
			s.entry.Error().Err(reacquireErr).Str("vip_uuid", req.GetVipUuid()).Msg("could not acquire again the ip of a vip still on its instance")
		}
		return nil, err
	}
	return &emptypb.Empty{}, nil
}

// acquireVipsIps acquires all the ips or none.
func (s *FlockServer) acquireVipsIps(ctx context.Context, zoneInfo *zone.Zone, vipsUuid []string) error {
	for i, vipUuid := range vipsUuid {
		err := s.acquireVipIp(ctx, zoneInfo, vipUuid)
		if err != nil {
			s.releaseVipsIps(ctx, zoneInfo, vipsUuid[:i])
			return err
		}
	}
	return nil
}

func (s *FlockServer) acquireVipIp(ctx context.Context, zoneInfo *zone.Zone, vipUuid string) error {
	if s.ipam == nil {
		return nil
	}
	addr, err := s.vipAddr(ctx, vipUuid)
	if err != nil {
		return err
	}
	err = s.ipam.AcquireSpecificIP(ctx, zoneInfo, addr, vipUuid)
	if err != nil {
		return connect.Errorf(ipamErrorCode(err), "could not acquire ip %s in zone %s: %v", addr, zoneInfo.GetName(), err).WithCause(err)
	}
	return nil
}

// ipamErrorCode tells the errors of the ipam the user can solve from an outage of the ipam
func ipamErrorCode(err error) connect.Code {
	if stderrors.Is(err, ipams.ErrPoolNotFound) {
		return connect.CodeNotFound
	}
	userErrors := []error{ipams.ErrIPAllocated, ipams.ErrIPOutOfRange, ipams.ErrPoolExhausted, ipams.ErrIPsOutOfRanges, ipams.ErrNoAllocatableIP}
	for _, userErr := range userErrors {
		if stderrors.Is(err, userErr) {
			return connect.CodeFailedPrecondition
		}
	}
	return connect.CodeInternal
}

func (s *FlockServer) releaseVipIp(ctx context.Context, zoneInfo *zone.Zone, vipUuid string) error {
	if s.ipam == nil {
		return nil
	}
	addr, err := s.vipAddr(ctx, vipUuid)
	if err != nil {
		return err
	}
	err = s.ipam.ReleaseIP(ctx, zoneInfo, addr)
	// an ip acquired before its zone had ranges, or by hand in a remote ipam, has nothing to release
	if err != nil && !stderrors.Is(err, ipams.ErrIPNotAllocated) {
		return connect.Errorf(connect.CodeInternal, "could not release ip %s in zone %s: %v", addr, zoneInfo.GetName(), err).WithCause(err)
	}
	return nil
}

// releaseVipsIps is for cleanups that can't fail anymore: an ip it could not release only needs a log.
func (s *FlockServer) releaseVipsIps(ctx context.Context, zoneInfo *zone.Zone, vipsUuid []string) {
	ctx, cancel := detachedContext(ctx)
	defer cancel()
	for _, vipUuid := range vipsUuid {
		err := s.releaseVipIp(ctx, zoneInfo, vipUuid)
		if err != nil {
			s.entry.Error().Err(err).Str("vip_uuid", vipUuid).Msg("could not release ip")
		}
	}
}

func (s *FlockServer) vipAddr(ctx context.Context, vipUuid string) (netip.Addr, error) {
	vipInfo, err := s.storeMan.VIPs().Read(ctx, vipUuid)
	if err != nil {
		return netip.Addr{}, errors.ToConnectError(err)
	}
	addr, err := netip.ParseAddr(vipInfo.GetIp())
	if err != nil {
		return netip.Addr{}, connect.Errorf(connect.CodeInternal, "could not parse ip of vip %s: %v", vipUuid, err).WithCause(err)
	}
	return addr, nil
}
