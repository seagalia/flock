package serves

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/samber/lo"
	"github.com/seagalia/flock/controller/lockers"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	vip "github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/observe"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *FlockServer) CreateVIP(ctx context.Context, req *svcvip.CreateVIPRequest) (*svcvip.VIPResponse, error) {
	err := s.checkIsAdmin(ctx)
	if err != nil {
		return nil, err
	}
	vipInfo := req.GetVip()
	vipInfo.UUID = uuid.New().String()

	err = s.createVip(ctx, vipInfo)
	if err != nil {
		return nil, err
	}
	return &svcvip.VIPResponse{
		Vip: vipInfo,
	}, nil
}

func (s *FlockServer) createVip(ctx context.Context, vipInfo *vip.VIP) error {
	sig, err := helpers.MessageSignature(vipInfo)
	if err != nil {
		return connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	err = s.checkVipRequest(ctx, vipInfo)
	if err != nil {
		return err
	}

	err = s.checkSecretRefs(ctx, nil, vipInfo)
	if err != nil {
		return err
	}

	err = s.storeMan.VIPs().Create(ctx, vipInfo)
	if err != nil {
		return errors.ToConnectError(err)
	}

	// the vip is stored: what follows completes the change, it must not stop halfway when the client leaves
	ctx, cancel := detachedContext(ctx)
	defer cancel()

	err = s.addPermForUser(ctx, helpers.TypeUrl[*vip.VIP](), vipInfo.UUID)
	if err != nil {
		return err
	}

	observe.EmitSubject(observer.EventType_CREATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(vipInfo),
		Identifier: vipInfo.UUID,
		Signature:  sig,
	})
	return nil
}

func (s *FlockServer) checkVipRequest(ctx context.Context, vipInfo *vip.VIP) error {
	serverByUuid, err := s.virtualServersByUuid(ctx, vipInfo.GetVirtualServersUuid())
	if err != nil {
		return err
	}
	servers := make([]*vs.VirtualServer, 0, len(vipInfo.GetVirtualServersUuid()))
	for _, virtualServerUuid := range vipInfo.GetVirtualServersUuid() {
		// uuids are no secret, knowing the one of a virtual server must not be enough to announce it
		err = s.checkPermission(ctx, helpers.TypeUrl[*vs.VirtualServer](), virtualServerUuid, permission.Role_READER)
		if err != nil {
			return err
		}
		servers = append(servers, serverByUuid[virtualServerUuid])
	}
	return checkVipPorts(servers)
}

func (s *FlockServer) virtualServersByUuid(ctx context.Context, uuids []string) (map[string]*vs.VirtualServer, error) {
	serverByUuid := make(map[string]*vs.VirtualServer, len(uuids))
	// a list without filters would give every virtual server
	if len(uuids) == 0 {
		return serverByUuid, nil
	}
	filters := make([]*svccore.ListFilter, 0, len(uuids))
	for _, virtualServerUuid := range uuids {
		filters = append(filters, &svccore.ListFilter{Key: storers.FilterUuidKey, Operator: svccore.ListFilter_EQUAL, Value: virtualServerUuid})
	}
	servers, err := s.storeMan.VirtualServers().List(ctx, nil, filters...)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	for _, server := range servers {
		serverByUuid[server.GetUUID()] = server
	}
	missing := lo.Filter(uuids, func(virtualServerUuid string, _ int) bool {
		_, found := serverByUuid[virtualServerUuid]
		return !found
	})
	if len(missing) > 0 {
		return nil, connect.Errorf(connect.CodeInternal, "could not find virtual servers %s", strings.Join(missing, ", "))
	}
	return serverByUuid, nil
}

func (s *FlockServer) ReadVIP(ctx context.Context, req *svccore.UuidRequest) (*svcvip.VIPResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*vip.VIP](), req.GetUUID(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	vipInfo, err := s.storeMan.VIPs().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	return &svcvip.VIPResponse{
		Vip: vipInfo,
	}, nil
}

func (s *FlockServer) UpdateVIP(ctx context.Context, req *svcvip.UpdateVIPRequest) (*svcvip.VIPResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*vip.VIP](), req.GetUUID(), permission.Role_WRITER)
	if err != nil {
		return nil, err
	}

	isUserAdmin := s.checkIsAdmin(ctx) == nil

	prevVip, err := s.storeMan.VIPs().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	ipChanged := prevVip.GetIp() != req.GetVip().GetIp()
	if ipChanged && !isUserAdmin {
		return nil, connect.Errorf(connect.CodePermissionDenied, "user is not admin and cannot change vip ip")
	}
	// the ipam keeps the ip of the vip in the zones of its instances
	if ipChanged && s.ipam != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "the ip of a vip cannot change while the ipam is enabled, delete and create the vip again")
	}

	if prevVip.GetPrefixLength() != req.GetVip().GetPrefixLength() && !isUserAdmin {
		return nil, connect.Errorf(connect.CodePermissionDenied, "user is not admin and cannot change vip prefix length")
	}

	vipInfo := req.GetVip()
	vipInfo.UUID = req.GetUUID()

	err = checkNameChangedByOwner[*vip.VIP](ctx, s.storeMan, vipInfo, func() error {
		return s.checkPermission(ctx, helpers.TypeUrl[*vip.VIP](), req.GetUUID(), permission.Role_OWNER)
	})
	if err != nil {
		return nil, err
	}

	sig, err := helpers.MessageSignature(vipInfo)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	err = s.checkVipRequest(ctx, vipInfo)
	if err != nil {
		return nil, err
	}

	err = s.checkSecretRefs(ctx, prevVip, vipInfo)
	if err != nil {
		return nil, err
	}

	err = s.storeMan.VIPs().Update(ctx, vipInfo.UUID, vipInfo)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	s.spreadPermToMembers(ctx, helpers.TypeUrl[*vip.VIP](), vipInfo.UUID)

	observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(vipInfo),
		Identifier: vipInfo.UUID,
		Signature:  sig,
	})

	return &svcvip.VIPResponse{
		Vip: vipInfo,
	}, nil
}

func (s *FlockServer) DeleteVIP(ctx context.Context, req *svccore.UuidRequest) (*emptypb.Empty, error) {
	err := s.checkIsAdmin(ctx)
	if err != nil {
		return nil, err
	}

	// no instance may take the vip between the check and the release of its ips
	if err := s.locker.Lock(ctx, lockers.InstanceVips); err != nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "could not lock %s: %v", lockers.InstanceVips, err).WithCause(err)
	}
	defer s.locker.Unlock(ctx, lockers.InstanceVips)

	founds, err := s.storeMan.Instances().FindSrcWithForeignResource(ctx, helpers.TypeUrl[*vip.VIP](), req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	if len(founds) > 0 {
		return nil, connect.Errorf(connect.CodeFailedPrecondition,
			"Vip %s is used by instance(s) %s .", req.GetUUID(), strings.Join(founds, ", "),
		)
	}

	// a registered vip keeps its ip in the zone when it is on no instance
	if s.ipam != nil {
		err = s.ipam.ReleaseOwner(ctx, req.GetUUID())
		if err != nil {
			return nil, connect.Errorf(connect.CodeInternal, "could not release the ips of vip %s: %v", req.GetUUID(), err).WithCause(err)
		}
	}

	err = s.storeMan.VIPs().Delete(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	observe.EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrl[*vip.VIP](),
		Identifier: req.GetUUID(),
	})

	return &emptypb.Empty{}, nil
}

func (s *FlockServer) ListVIP(ctx context.Context, req *svcvip.ListVIPRequest) (*svcvip.ListVIPResponse, error) {
	vipList, err := s.storeMan.VIPs().List(ctx, s.makeListOptions(ctx, req), req.GetFilters()...)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	return &svcvip.ListVIPResponse{
		Vip: vipList,
	}, nil
}

func (s *FlockServer) RegisterVip(ctx context.Context, req *svcvip.RegisterVipRequest) (*svcvip.VIPResponse, error) {
	if s.ipam == nil {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "ipam is disabled, vip %s cannot be registered", req.GetName())
	}

	_, err := storers.FindByName[*vip.VIP](ctx, req.GetName(), s.storeMan.VIPs(), nil)
	if err == nil {
		return nil, connect.Errorf(connect.CodeAlreadyExists, "vip %s already exists", req.GetName())
	}

	zoneFound, err := s.registerVipZone(ctx, req)
	if err != nil {
		return nil, err
	}

	vipInfo := &vip.VIP{
		UUID:          uuid.New().String(),
		Name:          req.GetName(),
		TypedAnnounce: req.GetTypedAnnounce(),
	}
	pool, err := registerVipPool(zoneFound, req)
	if err != nil {
		return nil, err
	}
	ipAcquired, err := s.ipam.AcquireIP(ctx, zoneFound, pool.GetName(), vipInfo.GetUUID())
	if err != nil {
		return nil, connect.Errorf(ipamErrorCode(err), "failed to acquire an ip in zone %s: %v", zoneFound.GetName(), err).WithCause(err)
	}
	vipInfo.Ip = ipAcquired.String()
	vipInfo.PrefixLength = int32(ipAcquired.BitLen())

	err = s.createVip(ctx, vipInfo)
	if err != nil {
		s.releaseIpsOfUnstoredVip(ctx, vipInfo.GetUUID())
		return nil, err
	}

	if req.GetInstanceUuid() == "" {
		return &svcvip.VIPResponse{Vip: vipInfo}, nil
	}

	if err := s.locker.Lock(ctx, lockers.InstanceVips); err != nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "could not lock %s: %v", lockers.InstanceVips, err).WithCause(err)
	}
	defer s.locker.Unlock(ctx, lockers.InstanceVips)
	err = s.attachVip(ctx, req.GetInstanceUuid(), vipInfo.GetUUID())
	if err != nil {
		return nil, connect.Errorf(connect.CodeOf(err),
			"vip %s registered with ip %s but not added to instance %s: %v",
			vipInfo.GetName(), vipInfo.GetIp(), req.GetInstanceUuid(), err,
		)
	}
	return &svcvip.VIPResponse{Vip: vipInfo}, nil
}

// releaseIpsOfUnstoredVip releases the ip a failed creation acquired: a vip stored before the failure
// (its permissions failed) keeps it, an ip is never released under a vip that exists.
func (s *FlockServer) releaseIpsOfUnstoredVip(ctx context.Context, vipUuid string) {
	ctx, cancel := detachedContext(ctx)
	defer cancel()
	stored, err := s.storeMan.VIPs().Exists(ctx, vipUuid)
	if err != nil || stored {
		return
	}
	err = s.ipam.ReleaseOwner(ctx, vipUuid)
	if err != nil {
		s.entry.Error().Err(err).Str("vip_uuid", vipUuid).Msg("could not release ip")
	}
}

// registerVipZone gives the zone where a registered vip takes its ip, the user must be able to use it.
func (s *FlockServer) registerVipZone(ctx context.Context, req *svcvip.RegisterVipRequest) (*zone.Zone, error) {
	if req.GetInstanceUuid() != "" {
		err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetInstanceUuid(), permission.Role_WRITER)
		if err != nil {
			return nil, err
		}
		instanceInfo, err := s.storeMan.Instances().Read(ctx, req.GetInstanceUuid())
		if err != nil {
			return nil, errors.ToConnectError(err)
		}
		zoneFound, err := s.storeMan.Zones().Read(ctx, instanceInfo.GetZoneUuid())
		if err != nil {
			return nil, errors.ToConnectError(err)
		}
		return zoneFound, nil
	}

	zoneFound, err := s.findRegisterZone(ctx, req)
	if err != nil {
		return nil, err
	}
	err = s.checkPermission(ctx, helpers.TypeUrl[*zone.Zone](), zoneFound.GetUUID(), permission.Role_READER)
	if err != nil {
		return nil, err
	}
	return zoneFound, nil
}

// registerVipPool chooses the pool of the zone where the ip of a registered vip is acquired.
func registerVipPool(z *zone.Zone, req *svcvip.RegisterVipRequest) (*zone.IpPool, error) {
	// no pool to look in: a selector would only answer NotFound, which hides that the zone is unmanaged
	if z.CouldNotAllocateIP() {
		return nil, connect.Errorf(connect.CodeFailedPrecondition, "zone %s has no pools, the ipam can't give it an ip", z.GetName())
	}
	if req.GetPool() == nil {
		defaultPool := z.DefaultPool()
		if defaultPool == nil {
			return nil, connect.Errorf(connect.CodeFailedPrecondition, "zone %s has no public pool, give a pool", z.GetName())
		}
		return defaultPool, nil
	}
	poolMetadata := req.GetPoolMetadata().GetMetadata()
	found := z.FindPool(req.GetPoolName(), poolMetadata)
	if found == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "zone %s has no pool matching name %q and metadata %v",
			z.GetName(), req.GetPoolName(), poolMetadata)
	}
	return found, nil
}

// findRegisterZone reads the zone a registered vip names, by uuid or by metadata, when it names no instance
func (s *FlockServer) findRegisterZone(ctx context.Context, req *svcvip.RegisterVipRequest) (*zone.Zone, error) {
	if req.GetZoneUuid() != "" {
		zoneFound, err := s.storeMan.Zones().Read(ctx, req.GetZoneUuid())
		if err != nil {
			return nil, errors.ToConnectError(err)
		}
		return zoneFound, nil
	}
	filters := make([]*svccore.ListFilter, 0)
	zoneMetadata := req.GetZoneMetadata().GetMetadata()
	for k, v := range zoneMetadata {
		filters = append(filters, &svccore.ListFilter{
			Key:        k,
			Value:      v,
			Operator:   svccore.ListFilter_EQUAL,
			IsMetadata: true,
		})
	}
	foundZones, err := s.storeMan.Zones().List(ctx, &storers.ListOptions{
		Limit:  1,
		Offset: 0,
	}, filters...)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	if len(foundZones) == 0 {
		return nil, connect.Errorf(connect.CodeNotFound, "no zone found with metadata %v", zoneMetadata)
	}
	return foundZones[0], nil
}
