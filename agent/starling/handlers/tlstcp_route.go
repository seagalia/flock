package handlers

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/seagalia/flock/sdk/storers"

	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/agent/starling/backendctl"
	"github.com/seagalia/flock/agent/starling/envoyctl"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	"github.com/seagalia/flock/sdk/logging"
)

type cachedTlsTcpRoute struct {
	aVip        *vip.VIP
	virtServ    *vs.VirtualServer
	backends    []*backend.Backend
	routeConfig *vs.TlsTcpRoutesConfig
	agentOpts   *starling_vsopts.TlsTcpOptions
}

type TlsTcpRouteHandler struct {
	entry         zerolog.Logger
	jointer       backendctl.Jointer
	configWatcher *envoyctl.ConfigWatcher
	cachedRoutes  *sync.Map
	store         storers.StoreManager
}

// NewTlsTcpRouteHandler reads the backends of the routes in store.
func NewTlsTcpRouteHandler(
	jointer backendctl.Jointer, configWatcher *envoyctl.ConfigWatcher, store storers.StoreManager,
) *TlsTcpRouteHandler {
	return &TlsTcpRouteHandler{
		store:         store,
		entry:         logging.Component("tls_tcp_route_handler"),
		jointer:       jointer,
		configWatcher: configWatcher,
		cachedRoutes:  &sync.Map{},
	}
}

func (h *TlsTcpRouteHandler) CreateRoute(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, message *vs.TlsTcpRoutesConfig) error {

	opts, err := message.GetStarlingOptions()
	if err != nil {
		return fmt.Errorf("failed to retrieve agent options: %w", err)
	}

	tlsTcpRoute := &cachedTlsTcpRoute{
		aVip:        aVip,
		virtServ:    virtServ,
		routeConfig: message,
		agentOpts:   opts,
		backends:    make([]*backend.Backend, 0),
	}
	for _, rte := range message.GetRoutes() {
		aBackend, err := stores.RetryFindBackend(ctx, h.store, rte.GetBackendUuid())
		if err != nil {
			return err
		}
		tlsTcpRoute.backends = append(tlsTcpRoute.backends, aBackend)
		h.configWatcher.UseBackend(aVip, virtServ, aBackend)
		h.jointer.AddListener(aBackend.GetUUID(), h.configWatcher)
		h.jointer.AddEndpointListener(aBackend.GetUUID(), h.configWatcher)

	}

	h.configWatcher.AddUpdateTlsTcpListener(aVip, virtServ, message.GetRoutes(), opts)
	previousRaw, hasPrevious := h.cachedRoutes.Swap(h.cacheKey(virtServ.GetUUID(), aVip.GetUUID()), tlsTcpRoute)
	if !hasPrevious {
		return nil
	}
	return releaseDroppedEnvoyClusters(h.jointer, h.configWatcher, aVip, virtServ, previousRaw.(*cachedTlsTcpRoute).backends, tlsTcpRoute.backends)
}

func (h *TlsTcpRouteHandler) cacheKey(vsUUID, vipUUID string) string {
	return fmt.Sprintf("%s-%s", vsUUID,
		strings.Split(vipUUID, "-")[0],
	)
}

func (h *TlsTcpRouteHandler) DeleteRoute(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, message *vs.TlsTcpRoutesConfig) error {
	routeRaw, ok := h.cachedRoutes.LoadAndDelete(h.cacheKey(virtServ.GetUUID(), aVip.GetUUID()))
	if ok {
		for _, aBackend := range routeRaw.(*cachedTlsTcpRoute).backends {
			if err := releaseEnvoyCluster(h.jointer, h.configWatcher, aVip, virtServ, aBackend.GetUUID()); err != nil {
				return err
			}
		}
	}
	h.configWatcher.RemoveListener(aVip, virtServ)
	return nil
}

func (h *TlsTcpRouteHandler) UpdateRoute(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, message *vs.TlsTcpRoutesConfig) error {
	return h.CreateRoute(ctx, aVip, virtServ, message)
}

func (h *TlsTcpRouteHandler) Clean() error {
	var result error
	h.cachedRoutes.Range(func(key, value any) bool {
		h.entry.Debug().Msgf("Cleaning route %v", key)
		defer h.entry.Debug().Msgf("Finished Cleaning route %v", key)
		tlsTcpRoute := value.(*cachedTlsTcpRoute)
		err := h.DeleteRoute(context.Background(), tlsTcpRoute.aVip, tlsTcpRoute.virtServ, tlsTcpRoute.routeConfig)
		if err != nil {
			result = multierror.Append(result, err)
		}
		return true
	})
	return result
}
