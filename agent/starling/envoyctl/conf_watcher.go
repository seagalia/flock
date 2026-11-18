package envoyctl

import (
	"context"
	stderrors "errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/seagalia/flock/sdk/storers"

	"github.com/avast/retry-go/v5"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
	"google.golang.org/protobuf/proto"
)

const (
	waitAttempts         = 600
	waitDelay            = 100 * time.Millisecond
	requeueDelay         = 100 * time.Millisecond
	elemRequestQueueSize = 2000
)

type ConfigWatcher struct {
	entry      zerolog.Logger
	typedCache *sync.Map
	// this is not a sync map as we need to ensure ip append in order to ensure
	// we don't miss any ip when sending to envoy
	ipAddrCache map[string]*core.IPAddr
	// protect ipAddrCache which is shared by all backends
	ipAddrMutex  *sync.RWMutex
	deltaWatches map[string]*deltaWatch
	// protect deltaWatches which is shared by all resource types
	deltaWatchesMutex  *sync.Mutex
	typeSent           *sync.Map
	previousRessources *sync.Map
	shouldCleanIpCache *sync.Map
	globalDisableSnat  bool
	stateStore         *stores.StateStore
	// store a chan per type url to send request in order
	// this ensure we don't send a request before the previous one to envoy
	elemRequestChans *sync.Map
	// the virtual servers of a vip using each backend: route deletions and backend removals come
	// on different controller streams, the envoy cluster must go with whichever comes last
	backendUsers      map[backendUser]bool
	backendUsersMutex *sync.RWMutex
	// what each virtual server gives the listener of its port: envoy refuses two listeners on one address, so the
	// virtual servers sharing a port of a vip share one listener
	listenerParts      map[listenerPartKey]*listenerPart
	listenerPartsMutex *sync.RWMutex
	// time a load assignment may wait for its backend, as long as waitElem waits
	loadAssignmentWaitLimit time.Duration

	store storers.StoreManager

	ctx context.Context
}

// deltaWatch is a watch go-control-plane opened on an envoy stream. When the stream ends, go-control-plane cancels its
// watches then closes their channel, so a send must never follow the cancel.
type deltaWatch struct {
	request *cache.DeltaRequest

	// mutex makes the cancel and a send exclude each other
	mutex     sync.Mutex
	cancelled bool
	responses chan<- cache.DeltaResponse
}

func (w *deltaWatch) cancel() {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.cancelled = true
}

func (w *deltaWatch) isCancelled() bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.cancelled
}

// send never blocks under the mutex, a blocked send would block the cancel too. go-control-plane buffers a response
// per resource type and a watch gets one response at most, so errWatchFull means nobody reads the channel anymore.
func (w *deltaWatch) send(resp cache.DeltaResponse) error {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if w.cancelled {
		return errWatchCancelled
	}
	select {
	case w.responses <- resp:
		return nil
	default:
		return errWatchFull
	}
}

// logDroppedResponse tells why a watch dropped the response of elemKey, the next watch of its type gets it
func (c *ConfigWatcher) logDroppedResponse(elemKey envoyKey, err error) {
	if stderrors.Is(err, errWatchFull) {
		// go-control-plane drains the channel while its stream lives, so this one has no reader
		c.entry.Warn().Msgf("Response channel full for %v, waiting for the next watch", elemKey)
		return
	}
	c.entry.Debug().Msgf("Watch cancelled before the response for %v, waiting for the next one", elemKey)
}

type elemRequest struct {
	key             envoyKey
	waitElems       []envoyKey
	deleting        bool
	firstRequeuedAt time.Time
}

type HttpRoute struct {
	Name        string
	VirtualHost *route.VirtualHost
	Backends    []*backend.Backend
}

// NewConfigWatcher reads in store the resources whose envoy config envoy refused, to mark them errored in states.
func NewConfigWatcher(
	ctx context.Context, globalDisableSnat bool, store storers.StoreManager, states *stores.StateStore,
) *ConfigWatcher {
	return &ConfigWatcher{
		entry:                   logging.Component("config_watcher"),
		typedCache:              &sync.Map{},
		deltaWatches:            make(map[string]*deltaWatch),
		deltaWatchesMutex:       &sync.Mutex{},
		ipAddrCache:             make(map[string]*core.IPAddr),
		ipAddrMutex:             &sync.RWMutex{},
		typeSent:                &sync.Map{},
		previousRessources:      &sync.Map{},
		shouldCleanIpCache:      &sync.Map{},
		elemRequestChans:        &sync.Map{},
		backendUsers:            make(map[backendUser]bool),
		backendUsersMutex:       &sync.RWMutex{},
		listenerParts:           make(map[listenerPartKey]*listenerPart),
		listenerPartsMutex:      &sync.RWMutex{},
		globalDisableSnat:       globalDisableSnat,
		loadAssignmentWaitLimit: time.Minute,
		stateStore:              states,
		store:                   store,
		ctx:                     ctx,
	}
}

func (c *ConfigWatcher) CreateWatch(request *cache.Request, sub cache.Subscription, responses chan cache.Response) (cancel func(), err error) {
	c.entry.Warn().Msg("CreateWatch called but not implemented")
	return func() {}, nil
}

func (c *ConfigWatcher) sendElemRequest(key envoyKey, deleting bool, waitElems ...envoyKey) {
	typeUrl := key.typeUrl
	// states are kept with the flock resource type, the one the status service is asked for
	if flockTypeUrl := flockTypeUrlFromEnvoy(typeUrl); flockTypeUrl != "" {
		state := agentstatus.StatusResponse_ADDING
		if deleting {
			state = agentstatus.StatusResponse_REMOVING
		}
		for _, uuid := range c.flockUuidsOfKey(key) {
			c.stateStore.SetStateTyped(flockTypeUrl, uuid, state)
		}
	}

	elemRequestChan := c.retrieveElemRequestChan(typeUrl)
	req := &elemRequest{
		key:       key,
		waitElems: waitElems,
		deleting:  deleting,
	}
	select {
	case <-c.ctx.Done():
	case elemRequestChan <- req:
	}
}

func (c *ConfigWatcher) retrieveElemRequestChan(typeUrl string) chan *elemRequest {
	elemRequestChanRaw, _ := c.elemRequestChans.LoadOrStore(typeUrl, make(chan *elemRequest, elemRequestQueueSize))
	return elemRequestChanRaw.(chan *elemRequest)
}

func (c *ConfigWatcher) RunRequestQueues() {
	go c.runRequestQueue(c.ctx, helpers.TypeUrl[*clusterv3.Cluster]())
	go c.runRequestQueue(c.ctx, helpers.TypeUrl[*listenerv3.Listener]())
	go c.runRequestQueue(c.ctx, helpers.TypeUrl[*tlsv3.Secret]())
	go c.runRequestQueue(c.ctx, helpers.TypeUrl[*endpointv3.ClusterLoadAssignment]())
}

func (c *ConfigWatcher) runRequestQueue(ctx context.Context, typeUrl string) {
	c.entry.Debug().Msgf("Running request queue for %s", typeUrl)
	defer c.entry.Debug().Msgf("Stopped request queue for %s", typeUrl)
	elemRequestChan := c.retrieveElemRequestChan(typeUrl)
	isLoadAssignmentQueue := typeUrl == helpers.TypeUrl[*endpointv3.ClusterLoadAssignment]()
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-elemRequestChan:
			// select picks at random among ready cases, a request may still come out after the stop
			if ctx.Err() != nil {
				return
			}
			if req.deleting {
				c.requestDelete(req.key)
				continue
			}
			if isLoadAssignmentQueue && c.waitsForUnsentBackend(req) {
				// envoy accepts the next cluster only once the warming one got its load assignment,
				// which may be behind this one in the queue
				c.requeueOrDrop(elemRequestChan, req)
				continue
			}
			c.requestUpdate(req.key, req.waitElems)
		}
	}
}

func (c *ConfigWatcher) waitsForUnsentBackend(req *elemRequest) bool {
	if _, cached := c.typedCache.Load(req.key); !cached {
		return false
	}
	for _, key := range req.waitElems {
		if _, sent := c.typeSent.Load(key); !sent {
			return true
		}
	}
	return false
}

func (c *ConfigWatcher) requeueOrDrop(elemRequestChan chan *elemRequest, req *elemRequest) {
	if req.firstRequeuedAt.IsZero() {
		req.firstRequeuedAt = time.Now()
	}
	waitedTooLong := time.Since(req.firstRequeuedAt) > c.loadAssignmentWaitLimit
	if waitedTooLong {
		c.clearState(req.key)
		c.entry.Error().Msgf("Error waiting: %s still waits for %v, dropped", req.key, req.waitElems)
		return
	}
	time.AfterFunc(requeueDelay, func() {
		select {
		case <-c.ctx.Done():
		case elemRequestChan <- req:
		}
	})
}

// envoyKey identifies an envoy resource in the caches of the watcher.
type envoyKey struct {
	typeUrl string
	// name is the name envoy knows the resource by
	name string
}

func (k envoyKey) String() string {
	return k.typeUrl + "/" + k.name
}

func (c *ConfigWatcher) makeKey(typeUrl, name string) envoyKey {
	return envoyKey{typeUrl: typeUrl, name: name}
}

func (c *ConfigWatcher) clusterKey(backendUuid string) envoyKey {
	return c.makeKey(helpers.TypeUrl[*clusterv3.Cluster](), backendUuid)
}

func (c *ConfigWatcher) loadAssignmentKey(backendUuid string) envoyKey {
	return c.makeKey(helpers.TypeUrl[*endpointv3.ClusterLoadAssignment](), backendUuid)
}

func (c *ConfigWatcher) secretKey(certUuid string) envoyKey {
	return c.makeKey(helpers.TypeUrl[*tlsv3.Secret](), certUuid)
}

// flockUuidsOfKey gives the flock resources an envoy resource is made of: the virtual servers of a listener, the
// backend of a cluster or the certificate of a secret.
func (c *ConfigWatcher) flockUuidsOfKey(key envoyKey) []string {
	if key.typeUrl != helpers.TypeUrl[*listenerv3.Listener]() {
		return []string{key.name}
	}
	c.listenerPartsMutex.RLock()
	defer c.listenerPartsMutex.RUnlock()
	var uuids []string
	for _, part := range c.partsOfListener(key) {
		uuids = append(uuids, part.virtServ.GetUUID())
	}
	return uuids
}

func (c *ConfigWatcher) storeEnvoyErr(ctx context.Context, request *cache.DeltaRequest, sub cache.Subscription) {
	var envoyName string
	for envoyName = range sub.ReturnedResources() {
		break
	}
	for _, uuid := range c.flockUuidsOfKey(c.makeKey(request.GetTypeUrl(), envoyName)) {
		c.storeEnvoyErrOf(ctx, request, uuid)
	}
}

func (c *ConfigWatcher) storeEnvoyErrOf(ctx context.Context, request *cache.DeltaRequest, uuid string) {
	var mess proto.Message
	var err error
	switch request.GetTypeUrl() {
	case helpers.TypeUrl[*clusterv3.Cluster]():
		mess, err = c.store.Backends().Read(ctx, uuid)
		if err != nil {
			c.entry.Error().Msgf("error reading backend %s: %s", uuid, err.Error())
			return
		}
	case helpers.TypeUrl[*listenerv3.Listener]():
		mess, err = c.store.VirtualServers().Read(ctx, uuid)
		if err != nil {
			c.entry.Error().Msgf("error reading virtual server %s: %s", uuid, err.Error())
			return
		}
	case helpers.TypeUrl[*tlsv3.Secret]():
		mess, err = c.store.Certificates().Read(ctx, uuid)
		if err != nil {
			c.entry.Error().Msgf("error reading certificate %s: %s", uuid, err.Error())
			return
		}
	}
	if mess == nil {
		return
	}
	c.stateStore.AddError(mess, fmt.Errorf("error while sending %s to envoy: %s",
		request.GetTypeUrl(), request.GetErrorDetail().GetMessage()),
	)
}

func (c *ConfigWatcher) CreateDeltaWatch(request *cache.DeltaRequest, sub cache.Subscription, responses chan cache.DeltaResponse) (cancel func(), err error) {
	if request.ErrorDetail != nil {
		c.entry.Error().Msgf("envoy request error: %s", request.ErrorDetail.Message)
		stats.envoyResInError.WithLabelValues(request.GetTypeUrl()).Inc()
		c.storeEnvoyErr(c.ctx, request, sub)
	}

	for envoyName := range request.InitialResourceVersions {
		key := c.makeKey(request.GetTypeUrl(), envoyName)
		c.previousRessources.Store(key, true)
	}
	typeUrl := request.GetTypeUrl()
	watch := &deltaWatch{request: request, responses: responses}
	c.deltaWatchesMutex.Lock()
	c.deltaWatches[typeUrl] = watch
	c.deltaWatchesMutex.Unlock()
	c.resendSubscribed(request)
	return func() {
		watch.cancel()
		c.deltaWatchesMutex.Lock()
		defer c.deltaWatchesMutex.Unlock()
		// the stream of a restarted envoy may already have opened the next watch of this type
		if c.deltaWatches[typeUrl] == watch {
			delete(c.deltaWatches, typeUrl)
		}
	}, nil
}

// resendSubscribed is needed as envoy ignores a resource received before it subscribed to it,
// e.g. a certificate sent before the listener using it
func (c *ConfigWatcher) resendSubscribed(request *cache.DeltaRequest) {
	for _, name := range request.GetResourceNamesSubscribe() {
		key := c.makeKey(request.GetTypeUrl(), name)
		if _, cached := c.typedCache.Load(key); !cached {
			continue
		}
		// asynchronous, the request queue may be waiting for this watch
		go c.sendElemRequest(key, false)
	}
}

// waitRetryOpts end the waits when the watcher stops
func (c *ConfigWatcher) waitRetryOpts() []retry.Option {
	return []retry.Option{
		retry.Context(c.ctx),
		retry.Attempts(waitAttempts),
		retry.Delay(waitDelay),
		retry.DelayType(retry.FixedDelay),
		retry.LastErrorOnly(true),
	}
}

// retrieveDeltaWatch takes the watch of typeUrl out of the watches, the next response waits for the next request
func (c *ConfigWatcher) retrieveDeltaWatch(typeUrl string) (*deltaWatch, error) {
	var watch *deltaWatch
	entry := c.entry.With().Str("type", typeUrl).Logger()
	retryErr := retry.New(c.waitRetryOpts()...).Do(func() error {
		entry.Trace().Msg("waiting delta response")
		c.deltaWatchesMutex.Lock()
		defer c.deltaWatchesMutex.Unlock()
		var ok bool
		watch, ok = c.deltaWatches[typeUrl]
		if !ok {
			return errors.NewErrNotFound("DeltaResponseWatch", typeUrl)
		}
		delete(c.deltaWatches, typeUrl)
		entry.Debug().Msg("finish wait delta response")
		return nil
	})
	if retryErr != nil {
		return nil, retryErr
	}
	return watch, nil
}

var (
	errElemRemoved    = stderrors.New("element removed while waiting its dependencies")
	errWatchCancelled = stderrors.New("delta watch cancelled")
	errWatchFull      = stderrors.New("delta watch channel full")
)

func (c *ConfigWatcher) waitElems(ownerKey envoyKey, keys []envoyKey) error {
	var result error
	for _, key := range keys {
		err := c.waitElem(ownerKey, key)
		if stderrors.Is(err, errElemRemoved) {
			return err
		}
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("error waiting for %s: %s", key, err.Error()))
		}
	}
	return result
}

func (c *ConfigWatcher) waitElem(ownerKey, key envoyKey) error {
	retryErr := retry.New(c.waitRetryOpts()...).Do(func() error {
		// a removed element must not block the queue of its type
		if _, ok := c.typedCache.Load(ownerKey); !ok {
			return retry.Unrecoverable(errElemRemoved)
		}
		_, sent := c.typeSent.Load(key)
		if !sent {
			return errors.NewErrNotFound(key.typeUrl, key.name)
		}
		return nil
	})
	if retryErr != nil {
		return retryErr
	}
	return nil
}

func (c *ConfigWatcher) requestUpdate(elemKey envoyKey, waitElems []envoyKey) {
	defer c.clearState(elemKey)
	if len(waitElems) > 0 {
		if err := c.waitElems(elemKey, waitElems); err != nil {
			if stderrors.Is(err, errElemRemoved) {
				c.entry.Debug().Msgf("%s removed while waiting its dependencies, skipping update", elemKey)
				return
			}
			if c.ctx.Err() != nil {
				return
			}
			c.entry.Error().Msgf("Error waiting: %s", err)
			return
		}
	}
	elem, ok := c.typedCache.Load(elemKey)
	if !ok {
		return
	}
	message := elem.(proto.Message)
	// envoy sends the versions back keyed by resource name, in its initial resource versions
	envoyName := elemKey.name
	versionMap := make(map[string]string)
	sig, err := helpers.MessageSignature(message)
	if err == nil {
		versionMap[envoyName] = sig
	}
	sent := c.sendOnNextWatch(elemKey, func(watch *deltaWatch) *RawDeltaResponse {
		requestSig, ok := watch.request.InitialResourceVersions[envoyName]
		if ok && requestSig == sig {
			c.entry.Debug().Msgf("No change for %s", elemKey)
			return nil
		}
		return &RawDeltaResponse{
			DeltaRequest:      watch.request,
			SystemVersionInfo: envoyName,
			Resource:          message,
			NextVersionMap:    versionMap,
			Signature:         sig,
			Ctx:               c.ctx,
		}
	})
	if !sent {
		return
	}
	c.typeSent.Store(elemKey, true)
	c.previousRessources.Delete(elemKey)
	c.entry.Debug().Msgf("Update sent for %v", elemKey)
}

// clearState ends the ADDING or REMOVING state set by sendElemRequest, and a previous envoy error.
func (c *ConfigWatcher) clearState(elemKey envoyKey) {
	flockTypeUrl := flockTypeUrlFromEnvoy(elemKey.typeUrl)
	if flockTypeUrl == "" {
		return
	}
	for _, uuid := range c.flockUuidsOfKey(elemKey) {
		c.stateStore.ClearState(flockTypeUrl, uuid)
	}
}

func flockTypeUrlFromEnvoy(envoyTypeUrl string) string {
	switch envoyTypeUrl {
	case helpers.TypeUrl[*clusterv3.Cluster]():
		return helpers.TypeUrl[*backend.Backend]()
	case helpers.TypeUrl[*listenerv3.Listener]():
		return helpers.TypeUrl[*vs.VirtualServer]()
	case helpers.TypeUrl[*tlsv3.Secret]():
		return helpers.TypeUrl[*cert.Certificate]()
	}
	return ""
}

func (c *ConfigWatcher) requestDelete(elemKey envoyKey) {
	defer c.clearState(elemKey)
	sent := c.sendOnNextWatch(elemKey, func(watch *deltaWatch) *RawDeltaResponse {
		return &RawDeltaResponse{
			DeltaRequest:      watch.request,
			SystemVersionInfo: elemKey.name,
			RemovedResources:  []string{elemKey.name},
			Ctx:               c.ctx,
		}
	})
	if !sent {
		return
	}
	c.typeSent.Delete(elemKey)
	c.previousRessources.Delete(elemKey)
}

// sendOnNextWatch sends what build makes on the next watch of the type of elemKey, and returns false when no watch
// came in time or the watcher stopped. A watch cancelled or full once taken drops the response, it then goes on the
// watch of the next stream: each try waits for a new watch, so the loop never spins. build returns nil when envoy
// already has the resource, the watch then goes back for the next response of its type.
func (c *ConfigWatcher) sendOnNextWatch(elemKey envoyKey, build func(*deltaWatch) *RawDeltaResponse) bool {
	for {
		watch, err := c.retrieveDeltaWatch(elemKey.typeUrl)
		if err != nil {
			if c.ctx.Err() == nil {
				c.entry.Error().Msgf("Unable to send update for %s: %s", elemKey, err)
			}
			return false
		}
		resp := build(watch)
		if resp == nil {
			c.putBackDeltaWatch(elemKey.typeUrl, watch)
			return true
		}
		c.entry.Debug().Msgf("Sending %v", elemKey)
		err = watch.send(resp)
		if err == nil {
			return true
		}
		c.logDroppedResponse(elemKey, err)
	}
}

// putBackDeltaWatch gives an unused watch back, unless its stream ended or opened a newer one meanwhile
func (c *ConfigWatcher) putBackDeltaWatch(typeUrl string, watch *deltaWatch) {
	c.deltaWatchesMutex.Lock()
	defer c.deltaWatchesMutex.Unlock()
	_, hasNewer := c.deltaWatches[typeUrl]
	if hasNewer || watch.isCancelled() {
		return
	}
	c.deltaWatches[typeUrl] = watch
}

func (c *ConfigWatcher) AddUpdateBackend(aBackend *backend.Backend) {
	key := c.clusterKey(aBackend.GetUUID())

	var transport *corev3.TransportSocket

	transport = backendTlsToTransportSocket(aBackend)
	if aBackend.GetEnableProxyProtocol() {
		transport = backendProxyProtocolTransportSocket(aBackend.GetProxyProtocolVersion(), transport)
	}

	envoyCluster, err := makeEnvoyCluster(aBackend, transport)
	if err != nil {
		err = fmt.Errorf("unable to create envoy cluster: %w", err)
		c.stateStore.AddError(aBackend, err)
		log.Error().Msg(err.Error())
		return
	}
	c.typedCache.Store(
		key,
		envoyCluster,
	)

	c.sendElemRequest(key, false)
}

func (c *ConfigWatcher) RemoveBackend(uuid string) error {
	key := c.clusterKey(uuid)
	c.typedCache.Delete(key)
	c.sendElemRequest(key, true)
	return nil
}

type backendUser struct {
	backendUuid string
	vsUuid      string
	vipUuid     string
}

// UseBackend pushes the envoy cluster and keeps it while a listener of virtServ uses it
func (c *ConfigWatcher) UseBackend(aVip *vip.VIP, virtServ *vs.VirtualServer, aBackend *backend.Backend) {
	c.backendUsersMutex.Lock()
	c.backendUsers[backendUser{backendUuid: aBackend.GetUUID(), vsUuid: virtServ.GetUUID(), vipUuid: aVip.GetUUID()}] = true
	c.backendUsersMutex.Unlock()
	c.AddUpdateBackend(aBackend)
}

// ReleaseBackend removes the envoy cluster when its last user releases it, it then returns true
func (c *ConfigWatcher) ReleaseBackend(aVip *vip.VIP, virtServ *vs.VirtualServer, backendUuid string) bool {
	user := backendUser{backendUuid: backendUuid, vsUuid: virtServ.GetUUID(), vipUuid: aVip.GetUUID()}
	c.backendUsersMutex.Lock()
	wasUser := c.backendUsers[user]
	delete(c.backendUsers, user)
	isLastUser := wasUser && !c.hasBackendUser(backendUuid)
	c.backendUsersMutex.Unlock()
	if !isLastUser {
		return false
	}
	c.RemoveBackend(backendUuid) // nolint:errcheck
	return true
}

func (c *ConfigWatcher) isBackendUsed(backendUuid string) bool {
	c.backendUsersMutex.RLock()
	defer c.backendUsersMutex.RUnlock()
	return c.hasBackendUser(backendUuid)
}

// hasBackendUser tells if a virtual server still uses the backend. The caller holds backendUsersMutex.
func (c *ConfigWatcher) hasBackendUser(backendUuid string) bool {
	for user := range c.backendUsers {
		if user.backendUuid == backendUuid {
			return true
		}
	}
	return false
}

func (c *ConfigWatcher) AddUpdateHttpListener(aVip *vip.VIP, virtServ *vs.VirtualServer, routes []*HttpRoute, opts *starling_vsopts.HttpOptions) {
	// proxy protocol must be read first: the tls inspector peeks at the bytes after the header.
	listFilters := proxyProtocolListFilters(virtServ.GetHttpRoutesConfig().GetAcceptProxyProtocol())
	listFilters = append(listFilters, makeTlsInspectorListFilt())
	if c.globalDisableSnat || opts != nil && opts.GetDisableSnat() {
		listFilters = append(listFilters, makeOriginSrcListFilt())
	}
	if opts != nil && opts.GetDisableDnat() {
		listFilters = append(listFilters, makeOriginDstListFilt())
	}
	c.setListenerPart(aVip, &listenerPart{
		virtServ:     virtServ,
		filterChains: virtualHostToFilterChain(virtServ.GetUUID(), virtServ.GetHttpRoutesConfig().GetExternalAuths(), routes, opts, virtServ.GetStarlingRateLimit()),
		filters:      listFilters,
	})
}

func (c *ConfigWatcher) AddUpdateTlsTcpListener(aVip *vip.VIP, virtServ *vs.VirtualServer, routes []*vs.TlsTcpRouteConfig, opts *starling_vsopts.TlsTcpOptions) {
	// proxy protocol must be read first: the tls inspector peeks at the bytes after the header.
	listFilters := proxyProtocolListFilters(virtServ.GetTlsTcpRoutesConfig().GetAcceptProxyProtocol())
	listFilters = append(listFilters, makeTlsInspectorListFilt())
	if c.globalDisableSnat || opts != nil && opts.GetDisableSnat() {
		listFilters = append(listFilters, makeOriginSrcListFilt())
	}
	if opts != nil && opts.GetDisableDnat() {
		listFilters = append(listFilters, makeOriginDstListFilt())
	}
	c.setListenerPart(aVip, &listenerPart{
		virtServ:     virtServ,
		filterChains: tlsTcpFilterChain(virtServ.GetUUID(), routes),
		filters:      listFilters,
	})
}

func (c *ConfigWatcher) AddUpdateTcpListener(aVip *vip.VIP, virtServ *vs.VirtualServer, backendUuid string, opts *starling_vsopts.TcpUdpOptions) {
	withSnat := opts == nil || !opts.GetDisableSnat()

	listFilters := proxyProtocolListFilters(virtServ.GetTcpRouteConfig().GetAcceptProxyProtocol())
	if c.globalDisableSnat || !withSnat {
		listFilters = append(listFilters, makeOriginSrcListFilt())
	}

	if opts != nil && opts.GetDisableDnat() {
		listFilters = append(listFilters, makeOriginDstListFilt())
	}
	c.setListenerPart(aVip, &listenerPart{
		virtServ:     virtServ,
		filterChains: tcpFilterChain(virtServ.GetUUID(), backendUuid),
		filters:      listFilters,
	})
}

func (c *ConfigWatcher) AddUpdateUdpListener(aVip *vip.VIP, virtServ *vs.VirtualServer, backendUuid string, opts *starling_vsopts.TcpUdpOptions) {
	withSnat := opts == nil || !opts.GetDisableSnat()
	listFilters := make([]*listenerv3.ListenerFilter, 0)
	if c.globalDisableSnat || !withSnat {
		listFilters = append(listFilters, makeOriginSrcListFilt())
	}

	if opts != nil && opts.GetDisableDnat() {
		listFilters = append(listFilters, makeOriginDstListFilt())
	}
	// the proxy comes last, after the filters that change the addresses
	listFilters = append(listFilters, udpProxyListenerFilter(virtServ.GetUUID(), backendUuid))
	c.setListenerPart(aVip, &listenerPart{
		virtServ: virtServ,
		filters:  listFilters,
		isUdp:    true,
	})
}

func (c *ConfigWatcher) RemoveListener(aVip *vip.VIP, virtServ *vs.VirtualServer) {
	key := c.listenerKey(aVip, virtServ)
	partKey := listenerPartKey{listener: key, vsUuid: virtServ.GetUUID()}
	c.listenerPartsMutex.Lock()
	if _, ok := c.listenerParts[partKey]; !ok {
		c.listenerPartsMutex.Unlock()
		return
	}
	delete(c.listenerParts, partKey)
	isLastPart := len(c.partsOfListener(key)) == 0
	var waits []envoyKey
	if isLastPart {
		c.typedCache.Delete(key)
	} else {
		waits = c.storeListener(aVip, key)
	}
	c.listenerPartsMutex.Unlock()

	// the states follow the virtual servers of the listener, this one is not among them anymore
	c.stateStore.ClearState(helpers.TypeUrl[*vs.VirtualServer](), virtServ.GetUUID())
	c.sendElemRequest(key, isLastPart, waits...)
}

type listenerPartKey struct {
	listener envoyKey
	vsUuid   string
}

// listenerPart is what a virtual server gives the listener of its port.
type listenerPart struct {
	virtServ     *vs.VirtualServer
	filterChains []*listenerv3.FilterChain
	filters      []*listenerv3.ListenerFilter
	isUdp        bool
}

// listenerKey names a listener after its port: the listener of a port shared by virtual servers stays the same envoy
// resource while they come and go.
func (c *ConfigWatcher) listenerKey(aVip *vip.VIP, virtServ *vs.VirtualServer) envoyKey {
	protocol := "tcp"
	if virtServ.GetUdpRouteConfig() != nil {
		protocol = "udp"
	}
	// two vips of an agent can share the first segment of their uuid, their listeners must stay apart
	envoyName := fmt.Sprintf("%s-%d/%s", protocol, virtServ.GetPort(), aVip.GetUUID())
	return c.makeKey(helpers.TypeUrl[*listenerv3.Listener](), envoyName)
}

func (c *ConfigWatcher) setListenerPart(aVip *vip.VIP, part *listenerPart) {
	key := c.listenerKey(aVip, part.virtServ)
	c.listenerPartsMutex.Lock()
	c.listenerParts[listenerPartKey{listener: key, vsUuid: part.virtServ.GetUUID()}] = part
	waits := c.storeListener(aVip, key)
	// sendElemRequest reads the parts again to set the states of the virtual servers
	c.listenerPartsMutex.Unlock()

	c.sendElemRequest(key, false, waits...)
}

// partsOfListener gives the parts of a listener in the order of their virtual servers, so the listener only changes
// with them. The caller holds listenerPartsMutex.
func (c *ConfigWatcher) partsOfListener(key envoyKey) []*listenerPart {
	parts := []*listenerPart{}
	for partKey, part := range c.listenerParts {
		if partKey.listener == key {
			parts = append(parts, part)
		}
	}
	slices.SortFunc(parts, func(a, b *listenerPart) int {
		return strings.Compare(a.virtServ.GetUUID(), b.virtServ.GetUUID())
	})
	return parts
}

// storeListener caches the listener merged from its parts and returns the clusters it waits for. The caller holds
// listenerPartsMutex.
func (c *ConfigWatcher) storeListener(aVip *vip.VIP, key envoyKey) []envoyKey {
	parts := c.partsOfListener(key)
	chains := []*listenerv3.FilterChain{}
	waits := []envoyKey{}
	for _, part := range parts {
		chains = append(chains, part.filterChains...)
		for _, backendUuid := range part.virtServ.GetBackendUuids() {
			waits = append(waits, c.clusterKey(backendUuid))
		}
	}
	// the virtual servers of a port agree on the listener filters: the controller checks proxy protocol, the vs handler
	// the nat of their starling options
	first := parts[0]
	if first.isUdp {
		chains = nil
	}
	envoyName := key.name
	c.typedCache.Store(key, makeEnvoyListener(envoyName, aVip, first.virtServ, chains, first.isUdp, first.filters...))
	return waits
}

func (c *ConfigWatcher) AddUpdateLoadAssign(backendUuid string, ipAddresses []*core.IPAddr) {
	key := c.loadAssignmentKey(backendUuid)
	c.typedCache.Store(
		key,
		makeEnvoyClusterLoadAssignment(backendUuid, ipAddresses),
	)
	c.sendElemRequest(key, false, c.clusterKey(backendUuid))
}

func (c *ConfigWatcher) RemoveLoadAssign(backendUuid string) {
	key := c.loadAssignmentKey(backendUuid)
	c.typedCache.Delete(key)
	c.sendElemRequest(key, true)
}

func (c *ConfigWatcher) AddUpdateCertificate(certificate *cert.Certificate) {
	key := c.secretKey(certificate.GetUUID())
	c.typedCache.Store(
		key,
		&tlsv3.Secret{
			Name: certificate.GetUUID(),
			Type: &tlsv3.Secret_TlsCertificate{
				TlsCertificate: &tlsv3.TlsCertificate{
					CertificateChain: &corev3.DataSource{
						Specifier: &corev3.DataSource_InlineString{
							InlineString: certificate.GetCertChain(),
						},
					},
					PrivateKey: &corev3.DataSource{
						Specifier: &corev3.DataSource_InlineString{
							InlineString: certificate.GetPrivateKey(),
						},
					},
				},
			},
		},
	)
	c.sendElemRequest(key, false)
}

func (c *ConfigWatcher) RemoveCertificate(certUuid string) {
	key := c.secretKey(certUuid)
	c.typedCache.Delete(key)
	c.sendElemRequest(key, true)
}

func (c *ConfigWatcher) constructIpAddresses(backendUuid string) []*core.IPAddr {
	var ipAddresses []*core.IPAddr
	c.ipAddrMutex.RLock()
	defer c.ipAddrMutex.RUnlock()
	for key, ipAddr := range c.ipAddrCache {
		if strings.HasPrefix(key, backendUuid+"/") {
			ipAddresses = append(ipAddresses, ipAddr)
		}
	}
	return ipAddresses
}

func (c *ConfigWatcher) cleanIpAddresses(backendUuid string) {
	c.ipAddrMutex.Lock()
	toDelete := make([]string, 0)
	for key := range c.ipAddrCache {
		if strings.HasPrefix(key, backendUuid+"/") {
			toDelete = append(toDelete, key)
		}
	}
	for _, key := range toDelete {
		delete(c.ipAddrCache, key)
	}
	c.ipAddrMutex.Unlock()
	c.shouldCleanIpCache.Delete(backendUuid)
}

func (c *ConfigWatcher) OnStartup(backendDef *backend.Backend, ipAddresses []*core.IPAddr) {
	if len(ipAddresses) == 0 {
		c.shouldCleanIpCache.Store(backendDef.GetUUID(), true)
		// envoy accepts the next cluster only once a new one got a load assignment, even an empty one.
		// A restarted resolver (backend updated) gets none: envoy keeps its endpoints until health checks pass.
		key := c.loadAssignmentKey(backendDef.GetUUID())
		if _, sent := c.typeSent.Load(key); !sent {
			c.AddUpdateLoadAssign(backendDef.GetUUID(), nil)
		}
		return
	}
	c.cleanIpAddresses(backendDef.GetUUID())
	c.ipAddrMutex.Lock()
	for _, ipAddr := range ipAddresses {
		c.ipAddrCache[fmt.Sprintf("%s/%s", backendDef.GetUUID(), ipAddr.Format())] = ipAddr
	}
	c.ipAddrMutex.Unlock()
	c.AddUpdateLoadAssign(backendDef.GetUUID(), ipAddresses)
}

func (c *ConfigWatcher) OnEndpointAdded(backendDef *backend.Backend, ipAddr *core.IPAddr) {
	if _, ok := c.shouldCleanIpCache.Load(backendDef.GetUUID()); ok {
		c.cleanIpAddresses(backendDef.GetUUID())
	}
	c.ipAddrMutex.Lock()
	c.ipAddrCache[fmt.Sprintf("%s/%s", backendDef.GetUUID(), ipAddr.Format())] = ipAddr
	c.ipAddrMutex.Unlock()
	c.AddUpdateLoadAssign(backendDef.GetUUID(), c.constructIpAddresses(backendDef.GetUUID()))
}

func (c *ConfigWatcher) OnEndpointRemoved(backendDef *backend.Backend, ipAddr *core.IPAddr) {
	c.ipAddrMutex.Lock()
	delete(c.ipAddrCache, fmt.Sprintf("%s/%s", backendDef.GetUUID(), ipAddr.Format()))
	c.ipAddrMutex.Unlock()
	c.AddUpdateLoadAssign(backendDef.GetUUID(), c.constructIpAddresses(backendDef.GetUUID()))
}

// OnBackendAdded backend notifications are asynchronous, a late one must not recreate a released backend
func (c *ConfigWatcher) OnBackendAdded(aBackend *backend.Backend) {
	if !c.isBackendUsed(aBackend.GetUUID()) {
		return
	}
	c.AddUpdateBackend(aBackend)
}

func (c *ConfigWatcher) OnBackendUpdated(aBackend *backend.Backend) {
	if !c.isBackendUsed(aBackend.GetUUID()) {
		return
	}
	c.AddUpdateBackend(aBackend)
}

func (c *ConfigWatcher) OnBackendRemoved(aBackend *backend.Backend) {
	err := c.RemoveBackend(aBackend.GetUUID())
	if err != nil {
		return
	} // nolint:errcheck
}
