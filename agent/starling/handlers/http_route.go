package handlers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/seagalia/flock/sdk/storers"

	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/agent/starling/backendctl"
	"github.com/seagalia/flock/agent/starling/envoyctl"
	wafclient "github.com/seagalia/flock/agent/starling/waf/client"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	"github.com/seagalia/flock/sdk/logging"
	svcregister "github.com/seagalia/flock/sdk/starling_waf/services/register/v1"
)

// wafCallTimeout bounds a call to the WAF, which waits for its socket since it may start after the agent. The route
// handler is locked meanwhile.
const wafCallTimeout = 10 * time.Second

type cachedHttpRoute struct {
	aVip       *vip.VIP
	virtServ   *vs.VirtualServer
	backends   []*backend.Backend
	routes     []*envoyctl.HttpRoute
	agentOpts  *starling_vsopts.HttpOptions
	wafConfigs map[string]*svcregister.RegisterWafConfigRequest
}

type HttpRouteHandler struct {
	entry         zerolog.Logger
	jointer       backendctl.Jointer
	configWatcher *envoyctl.ConfigWatcher
	cachedRoutes  *sync.Map
	mutex         *sync.Mutex
	wafClient     *wafclient.Client
	store         storers.StoreManager
}

// NewHttpRouteHandler reads the backends of the routes in store.
func NewHttpRouteHandler(
	jointer backendctl.Jointer,
	configWatcher *envoyctl.ConfigWatcher,
	wafClient *wafclient.Client,
	store storers.StoreManager,
) *HttpRouteHandler {
	return &HttpRouteHandler{
		store:         store,
		entry:         logging.Component("http_route_handler"),
		jointer:       jointer,
		configWatcher: configWatcher,
		cachedRoutes:  &sync.Map{},
		mutex:         &sync.Mutex{},
		wafClient:     wafClient,
	}
}

func (h *HttpRouteHandler) retrieveAsHttpRoutes(ctx context.Context, routeName string, message *vs.HttpRoutesConfig) ([]*envoyctl.HttpRoute, error) {
	httpRoutes := make([]*envoyctl.HttpRoute, 0)
	for i, virtHost := range message.GetVirtualHosts() {
		backends := make([]*backend.Backend, 0)
		for _, backendUuid := range virtHost.GetBackendUuids() {
			aBackend, err := stores.RetryFindBackend(ctx, h.store, backendUuid)
			if err != nil {
				return nil, err
			}
			backends = append(backends, aBackend)
		}
		for _, rte := range virtHost.GetRoutes() {
			_, err := rte.GetStarlingOptions()
			if err != nil {
				return nil, fmt.Errorf("failed to retrieve agent options of route %s: %w", rte.GetName(), err)
			}
		}
		httpRoutes = append(httpRoutes, &envoyctl.HttpRoute{
			// envoy refuses a route configuration with two virtual hosts of the same name
			Name:        fmt.Sprintf("%s-%d", routeName, i),
			VirtualHost: virtHost,
			Backends:    backends,
		})
	}
	return httpRoutes, nil
}

func (h *HttpRouteHandler) cacheKey(message *vs.VirtualServer, aVip *vip.VIP) string {
	return h.cacheKeyRaw(message.GetUUID(), aVip.GetUUID())
}

func (h *HttpRouteHandler) cacheKeyRaw(vsUUID, vipUUID string) string {
	return fmt.Sprintf("%s-%s", vsUUID,
		strings.Split(vipUUID, "-")[0],
	)
}

func (h *HttpRouteHandler) CreateRoute(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, message *vs.HttpRoutesConfig) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	routes, err := h.retrieveAsHttpRoutes(ctx, virtServ.GetName(), message)
	if err != nil {
		return fmt.Errorf("failed to retrieve http routes: %w", err)
	}
	policyBackends := make([]*backend.Backend, 0, len(message.GetExternalAuths()))
	for _, policy := range message.GetExternalAuths() {
		aBackend, err := stores.RetryFindBackend(ctx, h.store, policy.GetBackendUuid())
		if err != nil {
			return fmt.Errorf("failed to retrieve backend of external auth %s: %w", policy.GetName(), err)
		}
		policyBackends = append(policyBackends, aBackend)
	}
	opts, err := message.GetStarlingOptions()
	if err != nil {
		return fmt.Errorf("failed to retrieve agent options: %w", err)
	}
	configs := wafConfigs(virtServ.GetUUID(), message)
	httpRoute := &cachedHttpRoute{
		wafConfigs: configs,
		aVip:       aVip,
		virtServ:   virtServ,
		routes:     routes,
		agentOpts:  opts,
		backends:   make([]*backend.Backend, 0),
	}
	for _, rte := range routes {
		for _, aBackend := range rte.Backends {
			httpRoute.backends = append(httpRoute.backends, aBackend)
			h.configWatcher.UseBackend(aVip, virtServ, aBackend)
			h.jointer.AddListener(aBackend.GetUUID(), h.configWatcher)
			h.jointer.AddEndpointListener(aBackend.GetUUID(), h.configWatcher)
		}
	}
	for _, aBackend := range policyBackends {
		httpRoute.backends = append(httpRoute.backends, aBackend)
		h.configWatcher.UseBackend(aVip, virtServ, aBackend)
		h.jointer.AddListener(aBackend.GetUUID(), h.configWatcher)
		h.jointer.AddEndpointListener(aBackend.GetUUID(), h.configWatcher)
	}

	h.registerWafConfigs(ctx, configs)

	h.configWatcher.AddUpdateHttpListener(aVip, virtServ, routes, opts)
	previousRaw, hasPrevious := h.cachedRoutes.Swap(h.cacheKey(virtServ, aVip), httpRoute)
	if !hasPrevious {
		return nil
	}
	previous := previousRaw.(*cachedHttpRoute)
	// after the listener update, so envoy no longer sends the dropped identifiers
	for identifier := range previous.wafConfigs {
		_, kept := configs[identifier]
		if !kept {
			h.unregisterWafConfig(ctx, identifier)
		}
	}
	return releaseDroppedEnvoyClusters(h.jointer, h.configWatcher, aVip, virtServ, previous.backends, httpRoute.backends)
}

func (h *HttpRouteHandler) DeleteRoute(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, message *vs.HttpRoutesConfig) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	wafIdentifiers, err := h.removeRoute(aVip, virtServ)
	if err != nil {
		return err
	}
	for _, identifier := range wafIdentifiers {
		h.unregisterWafConfig(ctx, identifier)
	}
	return nil
}

// removeRoute takes the route out of envoy and gives the identifiers of its WAF configs, which the caller unregisters
func (h *HttpRouteHandler) removeRoute(aVip *vip.VIP, virtServ *vs.VirtualServer) ([]string, error) {
	var wafIdentifiers []string
	routeRaw, ok := h.cachedRoutes.LoadAndDelete(h.cacheKey(virtServ, aVip))
	if ok {
		cached := routeRaw.(*cachedHttpRoute)
		for _, aBackend := range cached.backends {
			if err := releaseEnvoyCluster(h.jointer, h.configWatcher, aVip, virtServ, aBackend.GetUUID()); err != nil {
				return nil, err
			}
		}
		for identifier := range cached.wafConfigs {
			wafIdentifiers = append(wafIdentifiers, identifier)
		}
	}
	h.configWatcher.RemoveListener(aVip, virtServ)
	return wafIdentifiers, nil
}

func (h *HttpRouteHandler) UpdateRoute(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, message *vs.HttpRoutesConfig) error {
	return h.CreateRoute(ctx, aVip, virtServ, message)
}

func wafConfigs(vsUuid string, message *vs.HttpRoutesConfig) map[string]*svcregister.RegisterWafConfigRequest {
	configs := make(map[string]*svcregister.RegisterWafConfigRequest)
	for i, virtHost := range message.GetVirtualHosts() {
		for _, rte := range virtHost.GetRoutes() {
			// retrieveAsHttpRoutes already refused the routes whose options can't be read
			opt, _ := rte.GetStarlingOptions()
			waf := opt.GetWaf()
			hasOwnConfig := waf.GetDetectOnly() || waf.GetCustomRules() != ""
			if !hasOwnConfig {
				continue
			}
			identifier := envoyctl.WafIdentifier(vsUuid, i, rte.GetName())
			req := &svcregister.RegisterWafConfigRequest{
				Identifier: identifier,
				Config:     &svcregister.RegisterWafConfigRequest_CustomRules{CustomRules: waf.GetCustomRules()},
			}
			if waf.GetDetectOnly() {
				req.Config = &svcregister.RegisterWafConfigRequest_DetectOnly{DetectOnly: true}
			}
			configs[identifier] = req
		}
	}
	return configs
}

func (h *HttpRouteHandler) registerWafConfigs(ctx context.Context, configs map[string]*svcregister.RegisterWafConfigRequest) {
	for identifier, req := range configs {
		callCtx, cancel := context.WithTimeout(ctx, wafCallTimeout)
		_, err := h.wafClient.RegisterWafConfig(callCtx, req)
		cancel()
		if err != nil {
			h.entry.Error().Err(err).Str("waf_identifier", identifier).Msg("failed to register the WAF config of a route")
		}
	}
}

func (h *HttpRouteHandler) unregisterWafConfig(ctx context.Context, identifier string) {
	callCtx, cancel := context.WithTimeout(ctx, wafCallTimeout)
	defer cancel()
	_, err := h.wafClient.UnregisterWafConfig(callCtx, &svcregister.UnregisterWafConfigRequest{Identifier: identifier})
	if err != nil {
		h.entry.Error().Err(err).Str("waf_identifier", identifier).Msg("failed to unregister the WAF config of a route")
	}
}

// Clean leaves the WAF configs registered. The WAF stops with the agent and loses them anyway, and an unregister sent
// to a WAF already stopping would only wait out its timeout.
func (h *HttpRouteHandler) Clean() error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	var result error
	h.cachedRoutes.Range(func(key, value any) bool {
		h.entry.Debug().Msgf("Cleaning route %v", key)
		defer h.entry.Debug().Msgf("Finished Cleaning route %v", key)
		httpRoute := value.(*cachedHttpRoute)
		_, err := h.removeRoute(httpRoute.aVip, httpRoute.virtServ)
		if err != nil {
			result = multierror.Append(result, err)
		}
		return true
	})
	return result
}

func (h *HttpRouteHandler) Name() string {
	return "http_route"
}
