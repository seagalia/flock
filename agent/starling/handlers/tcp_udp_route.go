package handlers

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/seagalia/flock/sdk/storers"

	"github.com/cloudflare/ipvs"
	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/agent/starling/backendctl"
	"github.com/seagalia/flock/agent/starling/envoyctl"
	"github.com/seagalia/flock/agent/starling/ipvsctl"
	"github.com/seagalia/flock/agent/starling/nm"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	oerrors "github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
)

const (
	// backendUpdateTimeout bounds the update of a route after its backend changed, the listener gives no ctx
	backendUpdateTimeout = 30 * time.Second

	ipvsDataPathCode  = "starling.datapath.ipvs"
	envoyDataPathCode = "starling.datapath.envoy"
)

var vsTypeUrl = helpers.TypeUrl[*vs.VirtualServer]()

type backendUuidRetriever interface {
	GetBackendUuid() string
	GetStarlingOptions() (*starling_vsopts.TcpUdpOptions, error)
}

type cachedTcpUdpRoute struct {
	service     *ipvs.Service
	vs          *vs.VirtualServer
	aVip        *vip.VIP
	ipvsHandler *IpvsHandler
	opts        *starling_vsopts.TcpUdpOptions
	backendUuid string
	isEnvoy     bool
	mutex       *sync.Mutex
}

type TcpUdpRouteHandler[T backendUuidRetriever] struct {
	entry         zerolog.Logger
	cachedRoutes  *sync.Map
	protocol      ipvs.Protocol
	jointer       backendctl.Jointer
	netMan        nm.NetworkManager
	configWatcher *envoyctl.ConfigWatcher
	// forceEnvoy serves every route with envoy, ipvs is never used
	forceEnvoy   bool
	store        storers.StoreManager
	states       *stores.StateStore
	protocolName string
}

// dataPath is the engine serving a route, reason says why envoy serves it and is empty with ipvs
type dataPath struct {
	envoy  bool
	reason string
}

func newTcpUdpRouteHandler[T backendUuidRetriever](jointer backendctl.Jointer, configWatcher *envoyctl.ConfigWatcher, netMan nm.NetworkManager, protocol ipvs.Protocol, forceEnvoy bool, store storers.StoreManager, states *stores.StateStore) *TcpUdpRouteHandler[T] {
	protocolName := "tcp"
	if protocol == ipvs.UDP {
		protocolName = "udp"
	}
	return &TcpUdpRouteHandler[T]{
		entry:         logging.Component(fmt.Sprintf("%s_route_handler", protocolName)),
		cachedRoutes:  &sync.Map{},
		protocol:      protocol,
		jointer:       jointer,
		netMan:        netMan,
		configWatcher: configWatcher,
		forceEnvoy:    forceEnvoy,
		store:         store,
		states:        states,
		protocolName:  protocolName,
	}
}

func (h *TcpUdpRouteHandler[T]) serviceExists(aVip *vip.VIP, port uint32) bool {
	services, err := ipvsctl.Services()
	if err != nil {
		return false
	}
	aVipKey := h.makeKey(aVip, port)
	for _, service := range services {
		serviceKey := fmt.Sprintf("%s:%d", ipvsctl.ServiceToIpNet(service.Service), service.Port)
		if serviceKey == aVipKey && service.Protocol == h.protocol {
			return true
		}
	}
	return false
}

func (h *TcpUdpRouteHandler[T]) CreateRoute(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, message T) error {
	opts, err := message.GetStarlingOptions()
	if err != nil {
		return err
	}
	return h.createRouteByUuid(ctx, aVip, virtServ, message.GetBackendUuid(), opts)
}

func (h *TcpUdpRouteHandler[T]) createRouteByUuid(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, backendUuid string, opts *starling_vsopts.TcpUdpOptions) error {
	h.entry.Debug().Str("backend_uuid", backendUuid).Msg("Creating route")
	aBackend, err := stores.RetryFindBackend(ctx, h.store, backendUuid)
	if err != nil {
		return err
	}
	path := h.chooseDataPath(virtServ, aBackend)
	if path.envoy {
		err = h.createUpdateRouteEnvoy(ctx, aVip, virtServ, backendUuid, opts)
	} else {
		err = h.createRouteIpvs(ctx, aVip, virtServ, backendUuid, opts)
	}
	if err != nil {
		return err
	}
	h.noteDataPath(aVip, virtServ, path, opts)
	return nil
}

func (h *TcpUdpRouteHandler[T]) chooseDataPath(virtServ *vs.VirtualServer, aBackend *backend.Backend) dataPath {
	if h.forceEnvoy {
		return dataPath{envoy: true, reason: "served by envoy in dev mode"}
	}
	if err := ipvsctl.CanUseIPVSProxier(h.lbPolicyToScheduler(aBackend.GetLbPolicy())); err != nil {
		return dataPath{envoy: true, reason: fmt.Sprintf(
			"served by envoy, IPVS can't apply the load balancing policy %s of the backend (%v)", aBackend.GetLbPolicy(), err)}
	}
	// ipvs forwards packets in the kernel, it can neither read nor send a proxy protocol header
	if aBackend.GetEnableProxyProtocol() {
		return dataPath{envoy: true, reason: "served by envoy, the backend uses the PROXY protocol and IPVS can't send its header"}
	}
	if virtServ.GetTcpRouteConfig().GetAcceptProxyProtocol() {
		return dataPath{envoy: true, reason: "served by envoy, the virtual server accepts the PROXY protocol and IPVS can't read its header"}
	}
	return dataPath{}
}

// noteDataPath tells in the status of the virtual server which engine serves the route. It warns when envoy drops what
// the route expects from the kernel, because udp leaves the kernel or direct routing is lost.
func (h *TcpUdpRouteHandler[T]) noteDataPath(aVip *vip.VIP, virtServ *vs.VirtualServer, path dataPath, opts *starling_vsopts.TcpUdpOptions) {
	directRouting := opts.GetDisableDnat()
	note := &flocktype.Note{Level: flocktype.Note_INFO, Code: ipvsDataPathCode, Scope: h.routeScope(aVip, virtServ)}
	switch {
	case !path.envoy && directRouting:
		note.Message = "served by IPVS in the kernel with direct routing"
	case !path.envoy:
		note.Message = "served by IPVS in the kernel"
	default:
		note.Code = envoyDataPathCode
		note.Message = path.reason
		losesKernelPath := !h.forceEnvoy && (h.protocol == ipvs.UDP || directRouting)
		if losesKernelPath {
			note.Level = flocktype.Note_WARNING
		}
		if losesKernelPath && directRouting {
			note.Message += ", direct routing is not used"
		}
	}
	h.states.SetNotes(vsTypeUrl, virtServ.GetUUID(), dataPathSource(note.GetScope()), []*flocktype.Note{note})
}

func (h *TcpUdpRouteHandler[T]) forgetDataPath(aVip *vip.VIP, virtServ *vs.VirtualServer) {
	h.states.SetNotes(vsTypeUrl, virtServ.GetUUID(), dataPathSource(h.routeScope(aVip, virtServ)), nil)
}

func (h *TcpUdpRouteHandler[T]) routeScope(aVip *vip.VIP, virtServ *vs.VirtualServer) string {
	hostPort := net.JoinHostPort(aVip.ToIpNet().IP.String(), strconv.FormatUint(uint64(virtServ.GetPort()), 10))
	return hostPort + "/" + h.protocolName
}

// dataPathSource gives each route of a virtual server its own notes, the virtual server has one route per VIP
func dataPathSource(scope string) string {
	return "datapath " + scope
}

func (h *TcpUdpRouteHandler[T]) createUpdateRouteEnvoy(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, backendUuid string, opts *starling_vsopts.TcpUdpOptions) error {
	aBackend, err := stores.RetryFindBackend(ctx, h.store, backendUuid)
	if err != nil {
		return err
	}

	h.configWatcher.UseBackend(aVip, virtServ, aBackend)
	h.jointer.AddListener(aBackend.GetUUID(), h.configWatcher)
	h.jointer.AddListener(aBackend.GetUUID(), h)
	h.jointer.AddEndpointListener(aBackend.GetUUID(), h.configWatcher)

	if h.protocol == ipvs.TCP {
		h.configWatcher.AddUpdateTcpListener(aVip, virtServ, backendUuid, opts)
	} else {
		h.configWatcher.AddUpdateUdpListener(aVip, virtServ, backendUuid, opts)
	}

	h.cachedRoutes.Store(h.makeKey(aVip, virtServ.GetPort()), &cachedTcpUdpRoute{
		vs:          virtServ,
		aVip:        aVip,
		mutex:       &sync.Mutex{},
		backendUuid: backendUuid,
		isEnvoy:     true,
		opts:        opts,
	})

	return nil
}

func (h *TcpUdpRouteHandler[T]) createRouteIpvs(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, backendUuid string, opts *starling_vsopts.TcpUdpOptions) error {
	aBackend, err := stores.RetryFindBackend(ctx, h.store, backendUuid)
	if err != nil {
		return err
	}
	ipNet := aVip.ToIpNet()
	family := ipvs.INET
	if aVip.IsIPV6() {
		family = ipvs.INET6
	}
	withSnat := opts == nil || !opts.GetDisableSnat()

	if withSnat {
		err = h.netMan.AddSnat(aVip.ToIpNet(), uint16(virtServ.GetPort()), h.protocol == ipvs.UDP, virtServ.GetEnableL3Log())
		if err != nil {
			return fmt.Errorf("failed to add snat: %w", err)
		}
	}

	service := &ipvs.Service{
		Address:   ipvsctl.AddrFromIP(ipNet.IP),
		Netmask:   ipvsctl.MaskFromIPMask(ipNet.Mask),
		Scheduler: h.lbPolicyToScheduler(aBackend.GetLbPolicy()),
		Timeout:   uint32(aBackend.GetConnectTimeout().GetSeconds()),
		Port:      uint16(virtServ.GetPort()),
		Family:    family,
		Protocol:  h.protocol,
	}
	if h.serviceExists(aVip, virtServ.GetPort()) {
		err = ipvsctl.UpdateService(*service)
	} else {
		err = ipvsctl.CreateService(*service)
	}
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	withDnat := opts == nil || !opts.GetDisableDnat()
	fwdType := ipvs.Masquerade
	if !withDnat {
		fwdType = ipvs.DirectRoute
	}
	ipvsHandler := NewIpvsHandler(service, fwdType)
	h.jointer.AddEndpointListener(backendUuid, ipvsHandler)

	h.cachedRoutes.Store(h.makeKey(aVip, virtServ.GetPort()), &cachedTcpUdpRoute{
		service:     service,
		vs:          virtServ,
		aVip:        aVip,
		opts:        opts,
		ipvsHandler: ipvsHandler,
		mutex:       &sync.Mutex{},
		backendUuid: backendUuid,
	})
	h.jointer.AddListener(backendUuid, h)
	return nil
}

func (h *TcpUdpRouteHandler[T]) makeKey(aVip *vip.VIP, port uint32) string {
	return fmt.Sprintf("%s:%d", aVip.ToIpNet(), port)
}

func (h *TcpUdpRouteHandler[T]) DeleteRoute(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, message T) error {
	return h.deleteRoute(aVip, virtServ)
}

func (h *TcpUdpRouteHandler[T]) deleteRoute(aVip *vip.VIP, virtServ *vs.VirtualServer) error {
	routeRaw, ok := h.cachedRoutes.LoadAndDelete(h.makeKey(aVip, virtServ.GetPort()))
	if !ok {
		return nil
	}
	route := routeRaw.(*cachedTcpUdpRoute)
	h.entry.Debug().Str("backend_uuid", route.backendUuid).Msg("Deleting route")
	h.forgetDataPath(aVip, virtServ)
	h.jointer.RemoveListener(route.backendUuid, h)

	if !route.isEnvoy {
		return h.deleteRouteByBackendUuidIpvs(aVip, virtServ, route, route.backendUuid)
	}
	if err := releaseEnvoyCluster(h.jointer, h.configWatcher, aVip, virtServ, route.backendUuid); err != nil {
		return err
	}
	h.configWatcher.RemoveListener(aVip, virtServ)
	return nil
}

func (h *TcpUdpRouteHandler[T]) deleteRouteByBackendUuidIpvs(aVip *vip.VIP, virtServ *vs.VirtualServer, route *cachedTcpUdpRoute, backendUuid string) error {
	err := h.jointer.RemoveEndpointListener(backendUuid, route.ipvsHandler)
	if err != nil && !oerrors.IsErrNotFound(err) {
		return fmt.Errorf("failed to remove endpoint listener: %w", err)
	}
	err = ipvsctl.RemoveService(*route.service)
	if err != nil {
		return fmt.Errorf("failed to remove service: %w", err)
	}

	withSnat := route.opts == nil || !route.opts.GetDisableSnat()
	if withSnat {
		err = h.netMan.DelSnat(aVip.ToIpNet(), uint16(virtServ.GetPort()), h.protocol == ipvs.UDP, virtServ.GetEnableL3Log())
		if err != nil {
			return fmt.Errorf("failed to del snat: %w", err)
		}
	}
	return nil
}

func (h *TcpUdpRouteHandler[T]) UpdateRoute(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, message T) error {
	opts, err := message.GetStarlingOptions()
	if err != nil {
		return err
	}
	return h.updateRouteByBackendUuid(ctx, aVip, virtServ, message.GetBackendUuid(), opts)
}

func (h *TcpUdpRouteHandler[T]) updateRouteByBackendUuid(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, backendUuid string, opts *starling_vsopts.TcpUdpOptions) error {
	h.entry.Debug().Str("backend_uuid", backendUuid).Msg("Updating route")
	port := virtServ.GetPort()
	routeRaw, ok := h.cachedRoutes.Load(h.makeKey(aVip, port))
	if !ok {
		return nil
	}
	route := routeRaw.(*cachedTcpUdpRoute)

	aBackend, err := stores.RetryFindBackend(ctx, h.store, backendUuid)
	if err != nil {
		return err
	}

	path := h.chooseDataPath(virtServ, aBackend)
	if route.isEnvoy != path.envoy {
		err := h.deleteRoute(aVip, virtServ)
		if err != nil {
			return fmt.Errorf("failed to delete route by backend uuid: %w", err)
		}
		err = h.createRouteByUuid(ctx, aVip, virtServ, backendUuid, opts)
		if err != nil {
			return fmt.Errorf("failed to create route: %w", err)
		}
		return nil
	}

	if path.envoy {
		err = h.updateRouteByBackendUuidEnvoy(ctx, aVip, virtServ, route, backendUuid, opts)
	} else {
		err = h.updateRouteByBackendUuidIpvs(aVip, virtServ, route, aBackend, backendUuid, opts)
	}
	if err != nil {
		return err
	}
	h.noteDataPath(aVip, virtServ, path, opts)
	return nil
}

func (h *TcpUdpRouteHandler[T]) updateRouteByBackendUuidEnvoy(
	ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer,
	route *cachedTcpUdpRoute,
	backendUuid string,
	opts *starling_vsopts.TcpUdpOptions,
) error {
	err := h.createUpdateRouteEnvoy(ctx, aVip, virtServ, backendUuid, opts)
	if err != nil {
		return err
	}
	if route.backendUuid == backendUuid {
		return nil
	}
	h.jointer.RemoveListener(route.backendUuid, h)
	return releaseEnvoyCluster(h.jointer, h.configWatcher, aVip, virtServ, route.backendUuid)
}

func (h *TcpUdpRouteHandler[T]) updateRouteByBackendUuidIpvs(
	aVip *vip.VIP, virtServ *vs.VirtualServer,
	route *cachedTcpUdpRoute, aBackend *backend.Backend,
	backendUuid string,
	opts *starling_vsopts.TcpUdpOptions,
) error {
	port := virtServ.GetPort()
	ipNet := aVip.ToIpNet()
	family := ipvs.INET
	if aVip.IsIPV6() {
		family = ipvs.INET6
	}
	service := &ipvs.Service{
		Address:   ipvsctl.AddrFromIP(ipNet.IP),
		Netmask:   ipvsctl.MaskFromIPMask(ipNet.Mask),
		Scheduler: h.lbPolicyToScheduler(aBackend.GetLbPolicy()),
		Timeout:   uint32(aBackend.GetConnectTimeout().GetSeconds()),
		Port:      uint16(port),
		Family:    family,
		Protocol:  h.protocol,
	}
	err := ipvsctl.UpdateService(*service)
	if err != nil {
		return fmt.Errorf("failed to update service: %w", err)
	}

	withSnat := opts == nil || !opts.GetDisableSnat()
	rteWithSnat := route.opts == nil || !route.opts.GetDisableSnat()

	if rteWithSnat != withSnat && withSnat {
		err = h.netMan.AddSnat(aVip.ToIpNet(), uint16(port), h.protocol == ipvs.UDP, virtServ.GetEnableL3Log())
		if err != nil {
			return fmt.Errorf("failed to add snat: %w", err)
		}
	}
	if rteWithSnat != withSnat && !withSnat {
		err = h.netMan.DelSnat(aVip.ToIpNet(), uint16(port), h.protocol == ipvs.UDP, virtServ.GetEnableL3Log())
		if err != nil {
			return fmt.Errorf("failed to del snat: %w", err)
		}
	}

	withDnat := opts == nil || !opts.GetDisableDnat()
	rteWithDnat := route.opts == nil || !route.opts.GetDisableDnat()

	if rteWithDnat != withDnat {
		fwdType := ipvs.Masquerade
		if !withDnat {
			fwdType = ipvs.DirectRoute
		}
		err := route.ipvsHandler.SetForwardType(fwdType)
		if err != nil {
			return fmt.Errorf("failed to set forward type: %w", err)
		}
	}

	route.mutex.Lock()
	route.service = service
	route.vs = virtServ
	route.aVip = aVip
	route.opts = opts
	route.mutex.Unlock()
	if route.backendUuid != backendUuid {
		err := h.jointer.RemoveEndpointListener(route.backendUuid, route.ipvsHandler)
		if err != nil {
			return fmt.Errorf("failed to remove endpoint listener: %w", err)
		}
		h.jointer.AddEndpointListener(backendUuid, route.ipvsHandler)
	}
	return nil

}

func (h *TcpUdpRouteHandler[T]) OnBackendAdded(_ *backend.Backend) {
	// useless, only to implement the interface
}
func (h *TcpUdpRouteHandler[T]) OnBackendRemoved(_ *backend.Backend) {
	// useless, only to implement the interface
}

func (h *TcpUdpRouteHandler[T]) OnBackendUpdated(aBackend *backend.Backend) {
	h.cachedRoutes.Range(func(key, value any) bool {
		route := value.(*cachedTcpUdpRoute)
		if route.backendUuid != aBackend.GetUUID() {
			return true
		}
		// the backend listener has no ctx, the retry reading the backend needs a bound
		ctx, cancel := context.WithTimeout(context.Background(), backendUpdateTimeout)
		defer cancel()
		err := h.updateRouteByBackendUuid(ctx, route.aVip, route.vs, aBackend.GetUUID(), route.opts)
		if err != nil {
			h.entry.Error().
				Err(err).
				Msgf("Failed to update route by backend uuid %s for virtual server %s", aBackend.GetUUID(), route.aVip.ToIpNet())
		}
		return true
	})
}

func (h *TcpUdpRouteHandler[T]) Clean() error {
	var result error
	h.cachedRoutes.Range(func(key, value any) bool {
		h.entry.Debug().Msgf("Cleaning route %v", key)
		defer h.entry.Debug().Msgf("Finished Cleaning route %v", key)
		route := value.(*cachedTcpUdpRoute)
		if route.ipvsHandler != nil {
			err := route.ipvsHandler.Clean()
			if err != nil {
				result = multierror.Append(result, err)
				return true
			}
		}
		err := h.deleteRoute(route.aVip, route.vs)
		if err != nil {
			result = multierror.Append(result, err)
		}
		return true
	})
	return result
}

func (h *TcpUdpRouteHandler[T]) lbPolicyToScheduler(policy backend.Backend_LbPolicy) string {
	switch policy {
	case backend.Backend_ROUND_ROBIN:
		return "rr"
	case backend.Backend_WEIGHTED_ROUND_ROBIN:
		return "wrr"
	case backend.Backend_LEAST_REQUEST:
		return "lc"
	case backend.Backend_WEIGHTED_LEAST_REQUEST:
		return "wlc"
	case backend.Backend_RING_HASH:
		return "sh"
	}
	// maglev by default
	return "mh"
}

func (h *TcpUdpRouteHandler[T]) GarbageCollect() error {
	services, err := ipvsctl.Services()
	if err != nil {
		if strings.Contains(err.Error(), "file does not exist") {
			// there is no services so ipvs is not enabled
			return nil
		}
		return fmt.Errorf("failed to get services: %w", err)
	}
	var result error
	for _, service := range services {
		if service.Protocol != h.protocol {
			continue
		}
		serviceKey := fmt.Sprintf("%s:%d", ipvsctl.ServiceToIpNet(service.Service), service.Port)
		found := false
		h.cachedRoutes.Range(func(key, _ any) bool {
			if key == serviceKey {
				found = true
				return false
			}
			return true
		})
		if found {
			continue
		}
		err := ipvsctl.RemoveService(service.Service)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to remove service %s: %w", serviceKey, err))
		}
	}
	return result
}

// NewTcpRouteHandler reads the backends of the routes in store and notes the data path of each route in states.
func NewTcpRouteHandler(jointer backendctl.Jointer, configWatcher *envoyctl.ConfigWatcher, netMan nm.NetworkManager, forceEnvoy bool, store storers.StoreManager, states *stores.StateStore) *TcpUdpRouteHandler[*vs.TcpRouteConfig] {
	return newTcpUdpRouteHandler[*vs.TcpRouteConfig](jointer, configWatcher, netMan, ipvs.TCP, forceEnvoy, store, states)
}

// NewUdpRouteHandler reads the backends of the routes in store and notes the data path of each route in states.
func NewUdpRouteHandler(jointer backendctl.Jointer, configWatcher *envoyctl.ConfigWatcher, netMan nm.NetworkManager, forceEnvoy bool, store storers.StoreManager, states *stores.StateStore) *TcpUdpRouteHandler[*vs.UdpRouteConfig] {
	return newTcpUdpRouteHandler[*vs.UdpRouteConfig](jointer, configWatcher, netMan, ipvs.UDP, forceEnvoy, store, states)
}
