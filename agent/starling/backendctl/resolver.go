package backendctl

import (
	"context"
	"fmt"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"math/rand/v2"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ArthurHlt/gohc"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/seagalia/flock/sdk/agentsdk/discoverers"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	// maxFirstCheckDelay spreads the first checks of endpoints added together, as a large config push does
	maxFirstCheckDelay = time.Second
	// intervalJitterDivisor adds up to interval/10 to each interval, so the checks of the agent don't stay in step
	intervalJitterDivisor = 10
)

//go:generate go tool mockgen -destination=../testhelpers/mock_endpoint_listener.go -package=testhelpers . EndpointListener

type EndpointListener interface {
	OnStartup(backendDef *backend.Backend, ipAddresses []*core.IPAddr)
	OnEndpointAdded(backendDef *backend.Backend, ipAddr *core.IPAddr)
	OnEndpointRemoved(backendDef *backend.Backend, ipAddr *core.IPAddr)
}

type endpointError struct {
	err error
}

func newEndpointError(err error) *endpointError {
	if err == nil {
		return &endpointError{
			err: nil,
		}
	}
	return &endpointError{
		err: fmt.Errorf("[%s] %w", time.Now().Format(time.RFC3339), err),
	}
}

func (ee *endpointError) Err() error {
	return ee.err
}

type endpoint struct {
	ipAddr *core.IPAddr
	ctx    context.Context
	cancel context.CancelFunc
	status *endpointStatus
	hc     *core.HealthCheck
	// nextCheck is the timer of the coming check: no goroutine waits for it
	nextCheck atomic.Pointer[time.Timer]
	// the thresholds progress, only the running check touches it: checks of one endpoint never overlap
	checked      bool
	curHealthy   uint32
	curUnhealthy uint32
}

type endpointStatus struct {
	Healthy *atomic.Bool
	Err     *atomic.Pointer[endpointError]
}

type Resolver struct {
	backendDef      *backend.Backend
	endpointMap     *sync.Map
	listeners       map[string]EndpointListener
	makeHealthCheck func(hc *core.HealthCheck, tlsServerName, rootCa string) (gohc.HealthChecker, error)
	hchecker        gohc.HealthChecker // checks every endpoint of the backend, built once by Run
	hcErr           error              // why hchecker could not be built
	makeDiscoverer  func(*backend.Backend) (discoverers.Discoverer, error)
	// done is closed when Run returns, whatever the path, so a replaced resolver can be waited for
	done             chan struct{}
	logEntry         zerolog.Logger
	rootCa           string
	tlsServerName    string
	rwMutexListeners *sync.RWMutex
}

// NewResolver reports the discovery failures of backendDef in states.
func NewResolver(backendDef *backend.Backend, states *stores.StateStore) *Resolver {
	return &Resolver{
		backendDef:       backendDef,
		rwMutexListeners: &sync.RWMutex{},
		endpointMap:      &sync.Map{},
		done:             make(chan struct{}),
		rootCa:           backendDef.GetCa(),
		tlsServerName:    backendDef.GetSni(),
		listeners:        make(map[string]EndpointListener),
		makeHealthCheck:  makeHealthCheck,
		makeDiscoverer: func(aBackend *backend.Backend) (discoverers.Discoverer, error) {
			return discoverers.MakeDiscoverer(aBackend, states)
		},
		logEntry: log.Logger.With().Str("backend_uuid", backendDef.GetUUID()).Str("component", "backend_resolver").Logger(),
	}

}

// Done is closed once Run returned and its cleanup is finished.
func (cr *Resolver) Done() <-chan struct{} {
	return cr.done
}

func (cr *Resolver) SetHealthCheckFactory(makeHealthCheck func(hc *core.HealthCheck, tlsServerName, rootCa string) (gohc.HealthChecker, error)) {
	cr.makeHealthCheck = makeHealthCheck
}

func (cr *Resolver) SetDiscovererFactory(makeDiscoverer func(*backend.Backend) (discoverers.Discoverer, error)) {
	cr.makeDiscoverer = makeDiscoverer
}

func (cr *Resolver) AddListener(listener EndpointListener) {
	cr.rwMutexListeners.Lock()
	defer cr.rwMutexListeners.Unlock()
	listener.OnStartup(cr.backendDef, cr.HealthyEndpoints())
	cr.listeners[fmt.Sprintf("%p", listener)] = listener
}

func (cr *Resolver) HealthyEndpoints() []*core.IPAddr {
	ipAddresses := make([]*core.IPAddr, 0)
	cr.endpointMap.Range(func(key, value any) bool {
		ep := value.(*endpoint)
		if !ep.status.Healthy.Load() {
			return true
		}
		ipAddresses = append(ipAddresses, ep.ipAddr)
		return true
	})
	return ipAddresses
}

func (cr *Resolver) RemoveListener(listener EndpointListener) {
	cr.rwMutexListeners.Lock()
	defer cr.rwMutexListeners.Unlock()
	delete(cr.listeners, fmt.Sprintf("%p", listener))
}

func (cr *Resolver) ListListeners() []EndpointListener {
	cr.rwMutexListeners.RLock()
	defer cr.rwMutexListeners.RUnlock()
	listeners := make([]EndpointListener, 0, len(cr.listeners))
	for _, listener := range cr.listeners {
		listeners = append(listeners, listener)
	}
	return listeners
}

func (cr *Resolver) addEndpoint(ipAddr *core.IPAddr) {
	cr.logEntry.Debug().Msgf("add endpoint %v", ipAddr.Format())
	ctx, cancel := context.WithCancel(context.Background())
	ep := &endpoint{
		ipAddr: ipAddr,
		ctx:    ctx,
		cancel: cancel,
		status: &endpointStatus{
			Healthy: &atomic.Bool{},
			Err:     &atomic.Pointer[endpointError]{},
		},
		hc: cr.backendDef.GetHealthCheck(),
	}
	ep.status.Err.Store(newEndpointError(nil))
	cr.endpointMap.Store(ipAddr.Format(), ep)
	cr.addHealthCheck(ep)
}

func (cr *Resolver) IsHealthy() bool {
	healthy := true
	cr.endpointMap.Range(func(key, value any) bool {
		ep := value.(*endpoint)
		if !ep.status.Healthy.Load() {
			healthy = false
			return false
		}
		return true
	})
	return healthy
}

func (cr *Resolver) updateHealthyBackend() {
	if cr.IsHealthy() {
		stats.SetBackendHealthy(cr.backendDef.GetUUID())
		return
	}
	stats.SetBackendUnhealthy(cr.backendDef.GetUUID())
}

func (cr *Resolver) delEndpoint(ipAddr *core.IPAddr) {
	ep, ok := cr.endpointMap.Load(ipAddr.Format())
	if !ok {
		return
	}
	cr.delHealthCheck(ep.(*endpoint))
	cr.endpointMap.Delete(ipAddr.Format())
	// the listeners only know healthy endpoints, an unhealthy one was already announced removed
	wasHealthy := ep.(*endpoint).status.Healthy.Swap(false)
	if !wasHealthy {
		return
	}
	cr.rwMutexListeners.RLock()
	defer cr.rwMutexListeners.RUnlock()
	for _, listener := range cr.listeners {
		listener.OnEndpointRemoved(cr.backendDef, ipAddr)
	}
}

func (cr *Resolver) delHealthCheck(ep *endpoint) {
	cr.logEntry.Info().Str("endpoint", ep.ipAddr.Format()).Msg("stopping healthcheck")
	ep.cancel()
	nextCheck := ep.nextCheck.Load()
	if nextCheck != nil {
		nextCheck.Stop()
	}
}

func (cr *Resolver) addHealthCheck(ep *endpoint) {
	if cr.hcErr != nil {
		ep.status.Healthy.Store(false)
		ep.status.Err.Store(newEndpointError(cr.hcErr))
		return
	}
	cr.logEntry.Info().Str("endpoint", ep.ipAddr.Format()).Msg("starting healthcheck as unhealthy")
	ep.status.Err.Store(newEndpointError(fmt.Errorf("healthcheck not yet started")))
	cr.scheduleCheck(ep, firstCheckDelay(ep.hc.GetInterval().AsDuration()))
}

// scheduleCheck runs the next check of ep on a timer: a goroutine exists only while a check runs
func (cr *Resolver) scheduleCheck(ep *endpoint, delay time.Duration) {
	ep.nextCheck.Store(time.AfterFunc(delay, func() { cr.check(ep) }))
}

func firstCheckDelay(interval time.Duration) time.Duration {
	spread := min(interval, maxFirstCheckDelay)
	if spread <= 0 {
		return 0
	}
	return rand.N(spread)
}

func nextCheckDelay(interval time.Duration) time.Duration {
	jitter := interval / intervalJitterDivisor
	if jitter <= 0 {
		return interval
	}
	return interval + rand.N(jitter)
}

func (cr *Resolver) check(ep *endpoint) {
	// a timer that fired as the endpoint was removed
	if ep.ctx.Err() != nil {
		return
	}
	// only a single successful check is needed to mark the endpoint healthy at startup
	healthyTh := uint32(1)
	if ep.checked {
		healthyTh = ep.hc.GetHealthyThreshold()
	}
	ep.checked = true
	unhealthyTh := ep.hc.GetUnhealthyThreshold()
	if unhealthyTh == 0 {
		unhealthyTh = 1
	}

	cr.logEntry.Debug().Str("endpoint", ep.ipAddr.Format()).Msg("running healthcheck ...")
	err := cr.hchecker.Check(ep.ipAddr.Format())
	// a check still running when the endpoint is removed must not announce it again
	if ep.ctx.Err() != nil {
		return
	}
	if err == nil {
		cr.onCheckSucceeded(ep, healthyTh)
	} else {
		cr.onCheckFailed(ep, unhealthyTh, err)
	}

	interval := ep.hc.GetInterval().AsDuration()
	hasUnhealthyInterval := ep.hc.GetUnhealthyInterval() != nil && ep.hc.GetUnhealthyInterval().AsDuration() > 0
	if hasUnhealthyInterval && !ep.status.Healthy.Load() {
		interval = ep.hc.GetUnhealthyInterval().AsDuration()
	}
	cr.scheduleCheck(ep, nextCheckDelay(interval))
}

func (cr *Resolver) onCheckSucceeded(ep *endpoint, healthyTh uint32) {
	wasHealthy := ep.status.Healthy.Swap(true)
	ep.status.Err.Store(newEndpointError(nil))
	if ep.curHealthy < healthyTh {
		ep.curHealthy++
	}
	if !wasHealthy && ep.curHealthy == healthyTh {
		ep.curHealthy = 0
		cr.logEntry.Info().Str("endpoint", ep.ipAddr.Format()).Msg("change endpoint status to healthy")
		stats.SetEndpointHealthy(cr.backendDef.GetUUID(), ep.ipAddr.Format())
		cr.updateHealthyBackend()
		cr.rwMutexListeners.RLock()
		for _, listener := range cr.listeners {
			listener.OnEndpointAdded(cr.backendDef, ep.ipAddr)
		}
		cr.rwMutexListeners.RUnlock()
	}
	cr.logEntry.Debug().Str("endpoint", ep.ipAddr.Format()).Msg("finished running healthcheck")
}

func (cr *Resolver) onCheckFailed(ep *endpoint, unhealthyTh uint32, err error) {
	if ep.curUnhealthy < unhealthyTh-1 {
		ep.curUnhealthy++
		cr.logEntry.Debug().
			Uint32("unhealthy", ep.curUnhealthy).
			Uint32("unhealthy_threshold", unhealthyTh).
			Str("endpoint", ep.ipAddr.Format()).
			Err(err).
			Msg("healthcheck failed but not yet reaching unhealthy threshold")
		return
	}

	wasHealthy := ep.status.Healthy.Swap(false)
	ep.status.Err.Store(newEndpointError(err))
	ep.curUnhealthy = 0
	if wasHealthy {
		cr.logEntry.Warn().Str("endpoint", ep.ipAddr.Format()).Msg("change endpoint status to unhealthy")
		stats.SetEndpointUnhealthy(cr.backendDef.GetUUID(), ep.ipAddr.Format())
		cr.updateHealthyBackend()
		cr.rwMutexListeners.RLock()
		for _, listener := range cr.listeners {
			listener.OnEndpointRemoved(cr.backendDef, ep.ipAddr)
		}
		cr.rwMutexListeners.RUnlock()
	}
	cr.logEntry.Debug().Str("endpoint", ep.ipAddr.Format()).Err(err).Msg("healthcheck failed")
}

func (cr *Resolver) EndpointStats() []*backend.EndpointStat {
	stats := make([]*backend.EndpointStat, 0)
	cr.endpointMap.Range(func(key, value any) bool {
		ep := value.(*endpoint)
		epErr := ep.status.Err.Load()
		var lastErr *wrapperspb.StringValue
		if epErr != nil && epErr.Err() != nil {
			lastErr = &wrapperspb.StringValue{Value: epErr.Err().Error()}
		}
		stats = append(stats, &backend.EndpointStat{
			IpAddr:    ep.ipAddr,
			Healthy:   ep.status.Healthy.Load(),
			LastError: lastErr,
		})
		return true
	})
	return stats
}

// buildHealthCheck makes the checker shared by the endpoints: gohc checkers take the host on each check
func (cr *Resolver) buildHealthCheck() {
	hc := cr.backendDef.GetHealthCheck()
	cr.hchecker, cr.hcErr = cr.makeHealthCheck(hc, cr.tlsServerName, cr.rootCa)
	if cr.hcErr != nil {
		cr.logEntry.Error().Err(cr.hcErr).Msg("failed to create healthcheck")
		return
	}
	if cr.hchecker == nil {
		cr.hcErr = errors.NewErrNotImplemented(fmt.Sprintf("healthcheck '%s'", reflect.TypeOf(hc.GetHealthChecker())))
	}
}

func (cr *Resolver) Run(ctx context.Context) error {
	defer close(cr.done)
	stats.CleanForBackend(cr.backendDef.GetUUID())
	stats.SetBackendUnhealthy(cr.backendDef.GetUUID())
	cr.buildHealthCheck()
	disc, err := cr.makeDiscoverer(cr.backendDef)
	if err != nil {
		return err
	}
	found := make(chan []*core.IPAddr)
	go func() {
		defer close(found)
		for {
			err := disc.Run(ctx, found)
			if err == nil || ctx.Err() != nil {
				return
			}
			cr.logEntry.Error().Err(err).Msg("discoverer error")
			select {
			case <-ctx.Done():
				return
			case <-time.After(cr.backendDef.GetRefreshRate().AsDuration()):
			}
		}
	}()
	for ips := range found {
		cr.endpointMap.Range(func(key, value any) bool {
			ep := value.(*endpoint)
			found := false
			for _, ip := range ips {
				if ip.Format() == ep.ipAddr.Format() {
					found = true
					break
				}
			}
			if !found {
				cr.delEndpoint(ep.ipAddr)
				stats.DeleteEndpoint(cr.backendDef.GetUUID(), ep.ipAddr.Format())
			}
			return true
		})
		for _, ip := range ips {
			_, ok := cr.endpointMap.Load(ip.Format())
			if ok {
				continue
			}
			stats.SetEndpointUnhealthy(cr.backendDef.GetUUID(), ip.Format())
			cr.addEndpoint(ip)
		}
	}
	<-ctx.Done()

	cr.rwMutexListeners.Lock()
	listenersToDelete := make([]string, 0)
	for key := range cr.listeners {
		listenersToDelete = append(listenersToDelete, key)
	}
	for _, key := range listenersToDelete {
		delete(cr.listeners, key)
	}
	cr.rwMutexListeners.Unlock()

	cr.endpointMap.Range(func(key, value any) bool {
		ep := value.(*endpoint)
		cr.delEndpoint(ep.ipAddr)
		return true
	})

	return nil
}
