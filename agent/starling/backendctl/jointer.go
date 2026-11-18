package backendctl

import (
	"context"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
)

const (
	// previousResolverStopTimeout bounds the wait for a cancelled resolver to finish, so an update or a stop can't hold
	// the joint's lock forever on a resolver stuck in its cleanup. When it fires, the listeners already moved to the
	// new resolver while the old one may still emit to them for a moment.
	previousResolverStopTimeout = 30 * time.Second
)

type resolverStated struct {
	resolver    *Resolver
	backendUuid string
	ctx         context.Context
	cancel      context.CancelFunc
}

//go:generate go tool mockgen -destination=../testhelpers/mock_jointer.go -package=testhelpers . Jointer
//go:generate go tool mockgen -destination=../testhelpers/mock_jointer_listener.go -package=testhelpers . JointerListener

type JointerListener interface {
	OnBackendAdded(aBackend *backend.Backend)
	OnBackendRemoved(aBackend *backend.Backend)
	OnBackendUpdated(aBackend *backend.Backend)
}

type Jointer interface {
	AddListener(backendUuid string, listener JointerListener)
	RemoveListener(backendUuid string, listener JointerListener)
	AddEndpointListener(backendUuid string, listener EndpointListener)
	RemoveEndpointListener(backendUuid string, listener EndpointListener) error
	OnMessageAdded(ctx context.Context, message *backend.Backend) error
	OnMessageRemove(ctx context.Context, message *backend.Backend) error
}

type Joint struct {
	// ctx is the lifetime of the agent, every resolver is cancelled with it
	ctx            context.Context
	jointListeners *sync.Map
	// rwMutex guards the resolvers and the endpoint listeners waiting for one
	rwMutex         *sync.RWMutex
	resolversCached map[string]*resolverStated
	// waitingEndpointListeners belong to backends with no resolver yet. Virtual servers and backends come on different
	// streams, so a virtual server can be handled before the backend it uses.
	waitingEndpointListeners map[string][]EndpointListener
	logEntry                 zerolog.Logger
	states                   *stores.StateStore
}

// NewJoint makes resolvers reporting the discovery failures of the backends in states, they stop with ctx.
func NewJoint(ctx context.Context, states *stores.StateStore) *Joint {
	return &Joint{
		ctx:                      ctx,
		states:                   states,
		rwMutex:                  &sync.RWMutex{},
		jointListeners:           &sync.Map{},
		resolversCached:          make(map[string]*resolverStated),
		waitingEndpointListeners: make(map[string][]EndpointListener),
		logEntry:                 logging.Component("backend_joint"),
	}
}

func (j *Joint) AddListener(backendUuid string, listener JointerListener) {
	listeners, _ := j.jointListeners.LoadOrStore(backendUuid, []JointerListener{})
	if slices.Contains(listeners.([]JointerListener), listener) {
		return
	}
	listeners = append(listeners.([]JointerListener), listener)
	j.jointListeners.Store(backendUuid, listeners)
}

func (j *Joint) RemoveListener(backendUuid string, listener JointerListener) {
	listeners, _ := j.jointListeners.LoadOrStore(backendUuid, []JointerListener{})
	found := -1
	for index, currListener := range listeners.([]JointerListener) {
		if currListener == listener {
			found = index
		}
	}
	if found == -1 {
		return
	}
	listeners = append(listeners.([]JointerListener)[:found], listeners.([]JointerListener)[found+1:]...)
	j.jointListeners.Store(backendUuid, listeners)
}

// AddEndpointListener gives the endpoints of the backend to listener, now or once the backend comes
func (j *Joint) AddEndpointListener(backendUuid string, listener EndpointListener) {
	j.rwMutex.Lock()
	defer j.rwMutex.Unlock()
	resolverState, ok := j.resolversCached[backendUuid]
	if ok {
		resolverState.resolver.AddListener(listener)
		return
	}
	waiting := j.waitingEndpointListeners[backendUuid]
	if !slices.Contains(waiting, listener) {
		j.waitingEndpointListeners[backendUuid] = append(waiting, listener)
	}
}

func (j *Joint) RemoveEndpointListener(backendUuid string, listener EndpointListener) error {
	j.rwMutex.Lock()
	defer j.rwMutex.Unlock()
	resolverState, ok := j.resolversCached[backendUuid]
	if ok {
		resolverState.resolver.RemoveListener(listener)
		return nil
	}
	waiting := j.waitingEndpointListeners[backendUuid]
	index := slices.Index(waiting, listener)
	if index == -1 {
		return errors.NewErrNotFound("backend", backendUuid)
	}
	j.waitingEndpointListeners[backendUuid] = slices.Delete(waiting, index, index+1)
	return nil
}

func (j *Joint) OnMessageAdded(ctx context.Context, aBackend *backend.Backend) error {
	backendUuid := helpers.GetIdentifier(aBackend)
	j.rwMutex.Lock()
	defer j.rwMutex.Unlock()
	currResolverStated, hasResolver := j.resolversCached[backendUuid]
	resolver := NewResolver(aBackend, j.states)
	resolveCtx, cancelFunc := context.WithCancel(j.ctx)
	go func(ctx context.Context) {
		j.logEntry.Debug().Str("backend_uuid", aBackend.UUID).Msg("Starting resolver")
		err := resolver.Run(ctx)
		if err != nil {
			j.logEntry.Error().Msgf("resolver.Run() failed: %v", err)
		}
	}(resolveCtx)
	if hasResolver {
		// ensure clean is finished before adding to the new resolver
		prevListeners := currResolverStated.resolver.ListListeners()
		j.logEntry.Debug().Str("backend_uuid", aBackend.UUID).Msg("Stopping previous resolver")
		currResolverStated.cancel()
		select {
		case <-currResolverStated.resolver.Done():
			j.logEntry.Debug().Str("backend_uuid", aBackend.UUID).Msg("Resolver finished cleaning")
		case <-time.After(previousResolverStopTimeout):
			j.logEntry.Warn().Str("backend_uuid", aBackend.UUID).Msg("Previous resolver did not finish in time")
		}
		for _, listener := range prevListeners {
			resolver.AddListener(listener)
		}
		go j.emitBackendUpdated(aBackend)
	} else {
		for _, listener := range j.waitingEndpointListeners[backendUuid] {
			resolver.AddListener(listener)
		}
		delete(j.waitingEndpointListeners, backendUuid)
		go j.emitBackendAdded(aBackend)
	}
	j.resolversCached[backendUuid] = &resolverStated{
		resolver:    resolver,
		ctx:         resolveCtx,
		cancel:      cancelFunc,
		backendUuid: backendUuid,
	}
	return nil
}

func (j *Joint) getListener(backendUuid string) []JointerListener {
	listeners, _ := j.jointListeners.Load(backendUuid)
	if listeners == nil {
		return []JointerListener{}
	}
	return listeners.([]JointerListener)
}

func (j *Joint) emitBackendAdded(aBackend *backend.Backend) {
	listeners := j.getListener(helpers.GetIdentifier(aBackend))
	for _, listener := range listeners {
		listener.OnBackendAdded(aBackend)
	}
}

func (j *Joint) emitBackendRemoved(aBackend *backend.Backend) {
	listeners := j.getListener(helpers.GetIdentifier(aBackend))
	for _, listener := range listeners {
		listener.OnBackendRemoved(aBackend)
	}
}

func (j *Joint) emitBackendUpdated(aBackend *backend.Backend) {
	listeners := j.getListener(helpers.GetIdentifier(aBackend))
	for _, listener := range listeners {
		listener.OnBackendUpdated(aBackend)
	}
}

func (j *Joint) EndpointStatsByBackends() []*backend.BackendEndpointStats {
	j.rwMutex.RLock()
	defer j.rwMutex.RUnlock()
	stats := make([]*backend.BackendEndpointStats, 0, len(j.resolversCached))
	for backendUuid, resolverState := range j.resolversCached {
		edpStats := resolverState.resolver.EndpointStats()
		isHealthy := true
		for _, edpStat := range edpStats {
			if !edpStat.Healthy {
				isHealthy = false
				break
			}
		}
		stats = append(stats,
			&backend.BackendEndpointStats{
				BackendUuid:   backendUuid,
				Healthy:       isHealthy,
				EndpointStats: resolverState.resolver.EndpointStats(),
			},
		)
	}
	return stats
}

func (j *Joint) EndpointStatsForBackend(backendUuid string) *backend.BackendEndpointStats {
	j.rwMutex.RLock()
	defer j.rwMutex.RUnlock()
	resolverState, ok := j.resolversCached[backendUuid]
	if !ok {
		return nil
	}
	edpStats := resolverState.resolver.EndpointStats()
	isHealthy := false
	for _, edpStat := range edpStats {
		if edpStat.Healthy {
			isHealthy = true
			break
		}
	}
	return &backend.BackendEndpointStats{
		BackendUuid:   backendUuid,
		Healthy:       isHealthy,
		EndpointStats: resolverState.resolver.EndpointStats(),
	}
}

func (j *Joint) OnMessageRemove(ctx context.Context, aBackend *backend.Backend) error {
	backendUuid := helpers.GetIdentifier(aBackend)
	j.rwMutex.Lock()
	defer j.rwMutex.Unlock()
	currResolverStated, hasResolver := j.resolversCached[backendUuid]
	if !hasResolver {
		return nil
	}
	j.logEntry.Debug().Str("backend_uuid", aBackend.UUID).Msg("Stopping resolver")
	currResolverStated.cancel()
	delete(j.resolversCached, backendUuid)
	go j.emitBackendRemoved(aBackend)
	return nil
}

func (j *Joint) Stop() error {
	j.rwMutex.Lock()
	defer j.rwMutex.Unlock()
	for _, resolverState := range j.resolversCached {
		j.logEntry.Debug().Str("backend_uuid", resolverState.backendUuid).Msg("Stopping resolver")
		resolverState.cancel()
	}
	// one timer for the whole wait, so a stop never takes more than the bound whatever the number of resolvers
	timeout := time.After(previousResolverStopTimeout)
	for _, resolverState := range j.resolversCached {
		select {
		case <-resolverState.resolver.Done():
		case <-timeout:
			j.logEntry.Warn().Strs("backend_uuids", j.runningBackends()).Msg("Resolvers did not finish in time")
			return j.reset()
		}
	}
	return j.reset()
}

// runningBackends lists the backends whose resolver has not finished, the caller holds the lock.
func (j *Joint) runningBackends() []string {
	running := make([]string, 0)
	for backendUuid, resolverState := range j.resolversCached {
		select {
		case <-resolverState.resolver.Done():
		default:
			running = append(running, backendUuid)
		}
	}
	return running
}

func (j *Joint) reset() error {
	j.jointListeners.Range(func(key, value any) bool {
		j.jointListeners.Delete(key)
		return true
	})
	j.resolversCached = make(map[string]*resolverStated)
	j.waitingEndpointListeners = make(map[string][]EndpointListener)
	return nil
}
