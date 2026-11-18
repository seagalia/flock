package announcers

import (
	"context"

	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/observe"
	"github.com/seagalia/flock/sdk/storers"
)

//go:generate go tool mockgen -destination=mocks/mock_linker.go -package=mocks . Linker

type Linker interface {
	Connect(
		instanceUuid string,
		typeUrl string,
		aListen Listener,
		initResVers map[string]string,
		filters svccore.ListFilters,
		init InitConnectFunc,
	) error
	Disconnect(instanceUuid string, typeUrl string, aListen Listener)
	DisconnectAll(instanceUuid string)
	ListConnections() map[string]int
}

type InitConnectFunc func(changes []*svcdiscovery.Resource, removes []string)

type announcerFollow struct {
	nbConnected *atomic.Int64
	announcer   *Announcer
}

func (af *announcerFollow) GetNbConnected() int64 {
	return af.nbConnected.Load()
}

type Link struct {
	ctx        context.Context
	sm         storers.StoreManager
	announcers *sync.Map
	entry      zerolog.Logger
}

func NewLink(ctx context.Context, sm storers.StoreManager) *Link {
	return &Link{
		ctx:        ctx,
		sm:         sm,
		announcers: &sync.Map{},
		entry:      logging.Component("linker"),
	}
}

func (m *Link) Connect(
	instanceUuid string,
	typeUrl string,
	aListen Listener,
	initResVers map[string]string,
	filters svccore.ListFilters,
	init InitConnectFunc,
) error {
	m.entry.Debug().Str("instance_uuid", instanceUuid).Str("type_url", typeUrl).Msg("Link Connect")
	candidate := NewAnnouncer(m.ctx, instanceUuid, m.sm)
	afRaw, loaded := m.announcers.LoadOrStore(instanceUuid, &announcerFollow{
		nbConnected: &atomic.Int64{},
		announcer:   candidate,
	})
	af := afRaw.(*announcerFollow)
	if loaded {
		// its ctx would stay registered on the controller's until the stop
		candidate.Close()
	} else {
		m.initAnnouncer(af.announcer)
	}
	af.announcer.LockReceive()
	defer af.announcer.UnlockReceive()
	if init != nil {
		newResources, toRemoves := af.announcer.SnapRessources(typeUrl, initResVers, filters...)
		go func() {
			init(newResources, toRemoves)
		}()
	}
	af.announcer.On(typeUrl, aListen, filters...)
	af.nbConnected.Add(1)
	return nil
}

func (m *Link) Disconnect(instanceUuid string, typeUrl string, aListen Listener) {
	m.entry.Debug().Str("instance_uuid", instanceUuid).Str("type_url", typeUrl).Msg("Link Disconnect")
	afRaw, ok := m.announcers.Load(instanceUuid)
	if !ok {
		return
	}
	af := afRaw.(*announcerFollow)
	af.announcer.Off(typeUrl, aListen)
	af.nbConnected.Add(-1)
	if af.nbConnected.Load() == 0 {
		observe.Off(observer.EventType_DELETE_SUBJECT, af.announcer)
		observe.Off(observer.EventType_CREATE_SUBJECT, af.announcer)
		observe.Off(observer.EventType_UPDATE_SUBJECT, af.announcer)
		af.announcer.Close()
		m.announcers.Delete(instanceUuid)
	}
}

func (m *Link) DisconnectAll(instanceUuid string) {
	m.entry.Debug().Str("instance_uuid", instanceUuid).Msg("Link DisconnectAll")
	afRaw, ok := m.announcers.Load(instanceUuid)
	if !ok {
		return
	}
	af := afRaw.(*announcerFollow)
	observe.Off(observer.EventType_DELETE_SUBJECT, af.announcer)
	observe.Off(observer.EventType_CREATE_SUBJECT, af.announcer)
	observe.Off(observer.EventType_UPDATE_SUBJECT, af.announcer)
	af.announcer.Close()
	m.announcers.Delete(instanceUuid)
}

func (m *Link) initAnnouncer(a *Announcer) {
	a.LockReceive()
	defer a.UnlockReceive()
	a.Init()
	observe.On(observer.EventType_DELETE_SUBJECT, a)
	observe.On(observer.EventType_CREATE_SUBJECT, a)
	observe.On(observer.EventType_UPDATE_SUBJECT, a)
}

func (m *Link) ListConnections() map[string]int {
	connections := make(map[string]int)
	m.announcers.Range(func(key, value any) bool {
		connections[key.(string)] = int(value.(*announcerFollow).nbConnected.Load())
		return true
	})
	return connections
}
