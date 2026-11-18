package handlers

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/seagalia/flock/sdk/agentsdk/stores"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/sdk/agentsdk/discoverers"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/logging"
	"google.golang.org/protobuf/proto"
)

// BackendHandler runs one discoverer per backend the BIG-IP can't resolve itself (EDS, DNS with its own nameservers)
// and keeps the endpoints it finds. The BIG-IP health checks them with its own monitors, the agent never does.
type BackendHandler struct {
	notify         func()
	makeDiscoverer func(*backend.Backend) (discoverers.Discoverer, error)
	discoveries    sync.WaitGroup

	mu   sync.Mutex
	runs map[string]*discovery
}

type discovery struct {
	backend    *backend.Backend
	cancel     context.CancelFunc
	endpoints  []*core.IPAddr
	discovered bool
}

// NewBackendHandler discovers the endpoints of the backends, reporting the discovery failures in states.
func NewBackendHandler(notify func(), states *stores.StateStore) *BackendHandler {
	makeDiscoverer := func(b *backend.Backend) (discoverers.Discoverer, error) {
		return discoverers.MakeDiscoverer(b, states)
	}
	return &BackendHandler{notify: notify, makeDiscoverer: makeDiscoverer, runs: map[string]*discovery{}}
}

func (h *BackendHandler) Name() string {
	return "f5_backend"
}

// OnMessageAdded starts the discovery of a backend, again when its definition changed. The endpoints found by the
// previous discovery stay until the new one finds its own, so a backend update never makes its tenant wait. An update
// notifies at once: the rest of the backend (health check, lb policy, options) is in the tenant too.
func (h *BackendHandler) OnMessageAdded(_ context.Context, b *backend.Backend) error {
	if !as3.AgentDiscovers(b) {
		h.forget(b.GetUUID())
		h.notify()
		return nil
	}
	disc, err := h.makeDiscoverer(b)
	if err != nil {
		return err
	}

	isUpdate := h.start(b, disc)
	if isUpdate {
		h.notify()
	}
	return nil
}

// start runs the discovery of a backend unless the running one already discovers the same way. It tells the backend
// was known.
func (h *BackendHandler) start(b *backend.Backend, disc discoverers.Discoverer) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	previous := h.runs[b.GetUUID()]
	if previous != nil && sameDiscovery(previous.backend, b) {
		previous.backend = b
		return true
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &discovery{backend: b, cancel: cancel}
	if previous != nil {
		previous.cancel()
		run.endpoints, run.discovered = previous.endpoints, previous.discovered
	}
	h.runs[b.GetUUID()] = run
	h.discoveries.Go(func() { h.discover(ctx, b.GetUUID(), run, disc) })
	return previous != nil
}

// sameDiscovery tells two definitions of a backend discover the same way, only its endpoint and refresh rate matter.
func sameDiscovery(a, b *backend.Backend) bool {
	discoveryOf := func(x *backend.Backend) *backend.Backend {
		return &backend.Backend{Endpoint: x.GetEndpoint(), RefreshRate: x.GetRefreshRate()}
	}
	return proto.Equal(discoveryOf(a), discoveryOf(b))
}

func (h *BackendHandler) OnMessageRemove(_ context.Context, b *backend.Backend) error {
	h.forget(b.GetUUID())
	h.notify()
	return nil
}

func (h *BackendHandler) forget(backendUUID string) {
	h.mu.Lock()
	run := h.runs[backendUUID]
	delete(h.runs, backendUUID)
	h.mu.Unlock()
	if run != nil {
		run.cancel()
	}
}

// Endpoints gives the endpoints of a backend, false until its first discovery.
func (h *BackendHandler) Endpoints(backendUUID string) ([]*core.IPAddr, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	run, ok := h.runs[backendUUID]
	if !ok || !run.discovered {
		return nil, false
	}
	return slices.Clone(run.endpoints), true
}

// Stop returns once every discovery has ended, so none notifies a reconciler stopped after it.
func (h *BackendHandler) Stop() error {
	h.mu.Lock()
	for _, run := range h.runs {
		run.cancel()
	}
	h.runs = map[string]*discovery{}
	h.mu.Unlock()
	h.discoveries.Wait()
	return nil
}

func (h *BackendHandler) discover(ctx context.Context, backendUUID string, run *discovery, disc discoverers.Discoverer) {
	entry := logging.Component("f5_backend").With().Str("backend", backendUUID).Logger()
	found := make(chan []*core.IPAddr)
	runDone := make(chan struct{})
	defer func() { <-runDone }()
	go func() {
		defer close(runDone)
		err := disc.Run(ctx, found)
		if err != nil && ctx.Err() == nil {
			entry.Error().Err(err).Msg("endpoint discovery stopped")
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case endpoints := <-found:
			if h.update(backendUUID, run, endpoints) {
				h.notify()
			}
		}
	}
}

// update stores what a discovery found and tells it changed. A discovery replaced in the meantime is ignored.
func (h *BackendHandler) update(backendUUID string, run *discovery, endpoints []*core.IPAddr) bool {
	endpoints = slices.Clone(endpoints)
	slices.SortFunc(endpoints, func(a, b *core.IPAddr) int { return strings.Compare(a.Format(), b.Format()) })

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runs[backendUUID] != run {
		return false
	}
	same := run.discovered && slices.EqualFunc(run.endpoints, endpoints, func(a, b *core.IPAddr) bool { return proto.Equal(a, b) })
	run.endpoints, run.discovered = endpoints, true
	return !same
}
