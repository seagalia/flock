package serves

import (
	"context"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/seagalia/flock/controller/ipams"
	"github.com/seagalia/flock/controller/lockers"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/observe"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *FlockServer) CreateZone(ctx context.Context, req *svczone.CreateZoneRequest) (*svczone.ZoneResponse, error) {
	err := s.checkIsAdmin(ctx)
	if err != nil {
		return nil, err
	}

	zoneInfo := req.GetZone()
	zoneInfo.UUID = uuid.New().String()
	sig, err := helpers.MessageSignature(zoneInfo)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	err = s.checkSecretRefs(ctx, nil, zoneInfo)
	if err != nil {
		return nil, err
	}

	err = ipams.ValidateZone(zoneInfo)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid zone %s: %v", zoneInfo.GetName(), err).WithCause(err)
	}
	if s.ipam != nil {
		err = s.ipam.SyncZone(ctx, zoneInfo)
		if err != nil {
			return nil, connect.Errorf(ipamErrorCode(err), "could not sync the ips of zone %s: %v", zoneInfo.GetName(), err).WithCause(err)
		}
	}

	err = s.storeMan.Zones().Create(ctx, zoneInfo)
	if err != nil {
		if s.ipam != nil {
			// the ranges of a zone never stored would stay in the ipam forever
			s.syncZoneBack(ctx, &zone.Zone{UUID: zoneInfo.GetUUID()})
		}
		return nil, errors.ToConnectError(err)
	}

	observe.EmitSubject(observer.EventType_CREATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(zoneInfo),
		Identifier: zoneInfo.GetUUID(),
		Signature:  sig,
	})

	return &svczone.ZoneResponse{
		Zone: zoneInfo,
	}, nil
}

func (s *FlockServer) ReadZone(ctx context.Context, req *svccore.UuidRequest) (*svczone.ZoneResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*zone.Zone](), req.GetUUID(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	aZone, err := s.storeMan.Zones().Read(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	return &svczone.ZoneResponse{
		Zone: aZone,
	}, nil
}

func (s *FlockServer) UpdateZone(ctx context.Context, req *svczone.UpdateZoneRequest) (*svczone.ZoneResponse, error) {
	err := s.checkIsAdmin(ctx)
	if err != nil {
		return nil, err
	}

	zoneI := req.GetZone()
	zoneI.UUID = req.GetUUID()

	// a pool change between the read of the stored pools and the store would be overwritten
	if err := s.locker.Lock(ctx, lockers.ZonePools); err != nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "could not lock %s: %v", lockers.ZonePools, err).WithCause(err)
	}
	defer s.locker.Unlock(ctx, lockers.ZonePools)
	stored, err := s.storeMan.Zones().Read(ctx, zoneI.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	// a whole zone replaced from an older read would drop the pools added meanwhile
	poolsKept := proto.Equal(&zone.Zone{Pools: stored.GetPools()}, &zone.Zone{Pools: zoneI.GetPools()})
	if !poolsKept {
		return nil, connect.Errorf(connect.CodeInvalidArgument,
			"pools change with add-pool-to-zone, update-pool-of-zone and delete-pool-from-zone",
		)
	}

	err = s.checkSecretRefs(ctx, stored, zoneI)
	if err != nil {
		return nil, err
	}

	sig, err := helpers.MessageSignature(zoneI)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	err = s.storeMan.Zones().Update(ctx, zoneI.GetUUID(), zoneI)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(zoneI),
		Identifier: zoneI.GetUUID(),
		Signature:  sig,
	})

	return &svczone.ZoneResponse{
		Zone: zoneI,
	}, nil
}

func (s *FlockServer) DeleteZone(ctx context.Context, req *svccore.UuidRequest) (*emptypb.Empty, error) {
	err := s.checkIsAdmin(ctx)
	if err != nil {
		return nil, err
	}

	err = s.checkPermission(ctx, helpers.TypeUrl[*zone.Zone](), req.GetUUID(), permission.Role_OWNER)
	if err != nil {
		return nil, err
	}

	founds, err := s.storeMan.Instances().FindSrcWithForeignResource(ctx, helpers.TypeUrl[*zone.Zone](), req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	if len(founds) > 0 {
		return nil, connect.Errorf(connect.CodeFailedPrecondition,
			"Zone %s is used by instance(s) %s .", req.GetUUID(), strings.Join(founds, ", "),
		)
	}

	// serialized with the pool changes, which sync the same ranges
	if err := s.locker.Lock(ctx, lockers.ZonePools); err != nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "could not lock %s: %v", lockers.ZonePools, err).WithCause(err)
	}
	defer s.locker.Unlock(ctx, lockers.ZonePools)

	if s.ipam != nil {
		// a zone without pools makes the ipam free its ranges, so a remote ipam learns the zone is gone
		err = s.ipam.SyncZone(ctx, &zone.Zone{UUID: req.GetUUID()})
		if err != nil {
			return nil, connect.Errorf(ipamErrorCode(err), "could not free the ipam ranges of zone %s: %v", req.GetUUID(), err).WithCause(err)
		}
	}

	err = s.storeMan.Zones().Delete(ctx, req.GetUUID())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	observe.EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrl[*zone.Zone](),
		Identifier: req.GetUUID(),
	})

	return &emptypb.Empty{}, nil
}

func (s *FlockServer) ListZone(ctx context.Context, req *svczone.ListZoneRequest) (*svczone.ListZoneResponse, error) {
	zones, err := s.storeMan.Zones().List(ctx, s.makeListOptions(ctx, req), req.GetFilters()...)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	return &svczone.ListZoneResponse{
		Zones: zones,
	}, nil
}

func (s *FlockServer) AddPoolToZone(ctx context.Context, req *svczone.AddPoolToZoneRequest) (*svczone.ZoneResponse, error) {
	changed, err := s.changeZonePools(ctx, req.GetUUID(), func(z *zone.Zone) error {
		if z.FindPool(req.GetPool().GetName(), nil) != nil {
			return connect.Errorf(connect.CodeAlreadyExists, "pool %s already exists in zone %s", req.GetPool().GetName(), z.GetName())
		}
		// appended: the default pool, the first public one, stays the same
		z.Pools = append(z.Pools, req.GetPool())
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &svczone.ZoneResponse{Zone: changed}, nil
}

func (s *FlockServer) UpdatePoolOfZone(ctx context.Context, req *svczone.UpdatePoolOfZoneRequest) (*svczone.ZoneResponse, error) {
	changed, err := s.changeZonePools(ctx, req.GetUUID(), func(z *zone.Zone) error {
		index := slices.IndexFunc(z.GetPools(), func(pool *zone.IpPool) bool {
			return pool.GetName() == req.GetPool().GetName()
		})
		if index < 0 {
			return connect.Errorf(connect.CodeNotFound, "pool %s not found in zone %s", req.GetPool().GetName(), z.GetName())
		}
		z.Pools[index] = req.GetPool()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &svczone.ZoneResponse{Zone: changed}, nil
}

func (s *FlockServer) DeletePoolFromZone(ctx context.Context, req *svczone.DeletePoolFromZoneRequest) (*svczone.ZoneResponse, error) {
	changed, err := s.changeZonePools(ctx, req.GetUUID(), func(z *zone.Zone) error {
		index := slices.IndexFunc(z.GetPools(), func(pool *zone.IpPool) bool {
			return pool.GetName() == req.GetPoolName()
		})
		if index < 0 {
			return connect.Errorf(connect.CodeNotFound, "pool %s not found in zone %s", req.GetPoolName(), z.GetName())
		}
		z.Pools = slices.Delete(z.Pools, index, index+1)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &svczone.ZoneResponse{Zone: changed}, nil
}

// changeZonePools applies change to the pools of the stored zone, makes the ipam follow, then stores the zone.
// change answers the connect error of a refused change.
func (s *FlockServer) changeZonePools(ctx context.Context, zoneUuid string, change func(z *zone.Zone) error) (*zone.Zone, error) {
	err := s.checkIsAdmin(ctx)
	if err != nil {
		return nil, err
	}

	// two changes from the same read would lose one of the pools
	if err := s.locker.Lock(ctx, lockers.ZonePools); err != nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "could not lock %s: %v", lockers.ZonePools, err).WithCause(err)
	}
	defer s.locker.Unlock(ctx, lockers.ZonePools)
	// the vips of the instances recorded with a first pool must not change before the zone is stored
	if err := s.locker.Lock(ctx, lockers.InstanceVips); err != nil {
		return nil, connect.Errorf(connect.CodeUnavailable, "could not lock %s: %v", lockers.InstanceVips, err).WithCause(err)
	}
	defer s.locker.Unlock(ctx, lockers.InstanceVips)

	storedZone, err := s.storeMan.Zones().Read(ctx, zoneUuid)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	newZone := proto.Clone(storedZone).(*zone.Zone)
	err = change(newZone)
	if err != nil {
		return nil, err
	}
	err = ipams.ValidateZone(newZone)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid pools for zone %s: %v", newZone.GetName(), err).WithCause(err)
	}
	sig, err := helpers.MessageSignature(newZone)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "could not create signature: %v", err).WithCause(err)
	}

	var recordedVips []string
	if s.ipam != nil {
		err = s.ipam.SyncZone(ctx, newZone)
		if err != nil {
			return nil, connect.Errorf(ipamErrorCode(err), "could not sync the pools of zone %s: %v", newZone.GetName(), err).WithCause(err)
		}
		getsFirstPool := storedZone.CouldNotAllocateIP() && !newZone.CouldNotAllocateIP()
		if getsFirstPool {
			recordedVips, err = s.recordVipsOfZone(ctx, storedZone, newZone)
			if err != nil {
				return nil, err
			}
		}
	}

	err = s.storeMan.Zones().Update(ctx, newZone.GetUUID(), newZone)
	if err != nil {
		if s.ipam != nil {
			s.undoRecordVips(ctx, storedZone, newZone, recordedVips)
		}
		return nil, errors.ToConnectError(err)
	}

	observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
		TypeUrl:    helpers.TypeUrlFromMessage(newZone),
		Identifier: newZone.GetUUID(),
		Signature:  sig,
	})
	return newZone, nil
}

// recordVipsOfZone acquires in newZone the ips the vips of its instances got while the zone had no pools,
// all of them or none: a refused one undoes the sync of newZone. It gives the vips recorded.
func (s *FlockServer) recordVipsOfZone(ctx context.Context, storedZone, newZone *zone.Zone) ([]string, error) {
	instancesUuid, err := s.storeMan.Instances().FindSrcWithForeignResource(ctx, helpers.TypeUrl[*zone.Zone](), newZone.GetUUID())
	if err != nil {
		s.syncZoneBack(ctx, storedZone)
		return nil, errors.ToConnectError(err)
	}

	var recorded []string
	for _, instanceUuid := range instancesUuid {
		instanceInfo, err := s.storeMan.Instances().Read(ctx, instanceUuid)
		if err != nil {
			s.undoRecordVips(ctx, storedZone, newZone, recorded)
			return nil, errors.ToConnectError(err)
		}
		for _, vipUuid := range instanceInfo.GetVipsUuid() {
			err = s.acquireVipIp(ctx, newZone, vipUuid)
			if err != nil {
				s.undoRecordVips(ctx, storedZone, newZone, recorded)
				return nil, connect.Errorf(connect.CodeOf(err),
					"vip %s of instance %s does not fit the first pool of zone %s: %v",
					vipUuid, instanceUuid, newZone.GetName(), err,
				)
			}
			recorded = append(recorded, vipUuid)
		}
	}
	return recorded, nil
}

// undoRecordVips gives the ipam back the stored zone. It releases the ips in newZone only: ReleaseOwner
// would also release them in the other zones of the vips.
func (s *FlockServer) undoRecordVips(ctx context.Context, storedZone, newZone *zone.Zone, recordedVips []string) {
	s.releaseVipsIps(ctx, newZone, recordedVips)
	s.syncZoneBack(ctx, storedZone)
}

// syncZoneBack gives the ipam back the ranges of a zone after a failure that can't fail anymore: a sync
// it could not undo only needs a log.
func (s *FlockServer) syncZoneBack(ctx context.Context, storedZone *zone.Zone) {
	ctx, cancel := detachedContext(ctx)
	defer cancel()
	err := s.ipam.SyncZone(ctx, storedZone)
	if err != nil {
		s.entry.Error().Err(err).Str("zone_uuid", storedZone.GetUUID()).Msg("could not sync back the ranges of a zone")
	}
}
