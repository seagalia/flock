package handlers

import (
	"context"
	"fmt"

	"github.com/seagalia/flock/sdk/storers"

	"github.com/hashicorp/go-multierror"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/agent/starling/nm"
	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"

	"strings"
	"sync"
)

type VsHandler struct {
	entry              zerolog.Logger
	tcpRouteHandler    RouteHandler[*vs.TcpRouteConfig]
	udpRouteHandler    RouteHandler[*vs.UdpRouteConfig]
	httpRouteHandler   RouteHandler[*vs.HttpRoutesConfig]
	tlsTcpRouteHandler RouteHandler[*vs.TlsTcpRoutesConfig]
	cachedVss          *sync.Map
	loadedVs           *sync.Map
	attachVipLock      *sync.Map
	vsLoadingLock      *sync.Map
	netMan             nm.NetworkManager
	store              storers.StoreManager
}

type cachedVs struct {
	aVip        *vip.VIP
	virtServ    *vs.VirtualServer
	routeConfig vs.IsRouteConfig
}

func NewVsHandler(
	netMan nm.NetworkManager,
	tcpRouteHandler RouteHandler[*vs.TcpRouteConfig],
	udpRouteHandler RouteHandler[*vs.UdpRouteConfig],
	httpRouteHandler RouteHandler[*vs.HttpRoutesConfig],
	tlsTcpRouteHandler RouteHandler[*vs.TlsTcpRoutesConfig],
	store storers.StoreManager,
) *VsHandler {
	return &VsHandler{
		store:              store,
		entry:              logging.Component("vs_handler"),
		tcpRouteHandler:    tcpRouteHandler,
		udpRouteHandler:    udpRouteHandler,
		httpRouteHandler:   httpRouteHandler,
		cachedVss:          &sync.Map{},
		loadedVs:           &sync.Map{},
		tlsTcpRouteHandler: tlsTcpRouteHandler,
		attachVipLock:      &sync.Map{},
		vsLoadingLock:      &sync.Map{},
		netMan:             netMan,
	}
}

func (h *VsHandler) retrieveVipLock(vipUUID string) *sync.Mutex {
	vipLock, _ := h.attachVipLock.LoadOrStore(vipUUID, &sync.Mutex{})
	return vipLock.(*sync.Mutex)
}

func (h *VsHandler) retrieveVsLoadingLock(vsUUID string) *sync.RWMutex {
	vipLock, _ := h.vsLoadingLock.LoadOrStore(vsUUID, &sync.RWMutex{})
	return vipLock.(*sync.RWMutex)
}

func (h *VsHandler) OnMessageAdded(ctx context.Context, message *vs.VirtualServer) error {
	h.retrieveVsLoadingLock(message.GetUUID()).Lock()
	defer h.retrieveVsLoadingLock(message.GetUUID()).Unlock()
	vips, err := stores.RetryFindVipsForVirtualServer(ctx, h.store, message.GetUUID())
	if err != nil {
		return fmt.Errorf("failed to find vip for virtual server: %w", err)
	}

	// lock all vips to avoid attach/detach during the process
	for _, aVip := range vips {
		vipLock := h.retrieveVipLock(aVip.GetUUID())
		vipLock.Lock()
	}

	var result error
	for _, aVip := range vips {
		err = h.addUpdateForVip(ctx, message, aVip)
		if err != nil {
			result = multierror.Append(result,
				fmt.Errorf("failed to add virtual server %s for vip %s: %w",
					message.GetUUID(), aVip.GetUUID(), err),
			)
		}
	}

	for _, aVip := range vips {
		vipLock := h.retrieveVipLock(aVip.GetUUID())
		vipLock.Unlock()
	}
	if result != nil {
		return result
	}
	h.loadedVs.Store(message.GetUUID(), true)
	return nil
}

func (h *VsHandler) cacheKey(message *vs.VirtualServer, aVip *vip.VIP) string {
	return h.cacheKeyRaw(message.GetUUID(), aVip.GetUUID())
}

func (h *VsHandler) cacheKeyRaw(vsUUID, vipUUID string) string {
	return fmt.Sprintf("%s-%s", vsUUID,
		strings.Split(vipUUID, "-")[0],
	)
}

func (h *VsHandler) isCacheKeyOfVs(cacheKey, vsUUID string) bool {
	return strings.HasPrefix(cacheKey, vsUUID+"-")
}

func (h *VsHandler) addUpdateLimits(current, next *vs.VirtualServer, aVip *vip.VIP, isUdp bool) error {
	if len(current.GetLimitConfig()) == 0 {
		return h.AddAllLimits(aVip, next, isUdp)
	}

	if len(next.GetLimitConfig()) == 0 {
		h.deleteAllLimits(aVip, current, isUdp)
		return nil
	}

	currentConnLimit := current.GetFirstConnLimit()
	nextConnLimit := next.GetFirstConnLimit()
	var currentConnLimitSig string
	var nextConnLimitSig string

	currentConnRateLimit := current.GetFirstConnRateLimit()
	nextConnRateLimit := next.GetFirstConnRateLimit()
	var currentConnRateLimitSig string
	var nextConnRateLimitSig string

	var err error

	if currentConnLimit != nil {
		currentConnLimitSig, err = helpers.MessageSignature(currentConnLimit)
		if err != nil {
			return fmt.Errorf("failed to get connection limit signature: %w", err)
		}
	}
	if nextConnLimit != nil {
		nextConnLimitSig, err = helpers.MessageSignature(nextConnLimit)
		if err != nil {
			return fmt.Errorf("failed to get connection limit signature: %w", err)
		}
	}
	if currentConnRateLimit != nil {
		currentConnRateLimitSig, err = helpers.MessageSignature(currentConnRateLimit)
		if err != nil {
			return fmt.Errorf("failed to get connection rate limit signature: %w", err)
		}
	}
	if nextConnRateLimit != nil {
		nextConnRateLimitSig, err = helpers.MessageSignature(nextConnRateLimit)
		if err != nil {
			return fmt.Errorf("failed to get connection rate limit signature: %w", err)
		}
	}

	var result error

	if currentConnLimitSig != nextConnLimitSig && currentConnLimitSig != "" {
		err := h.netMan.DelConnLimit(aVip.ToIpNet(), uint16(current.GetPort()), isUdp,
			currentConnLimit.GetMaxConnections(),
			currentConnLimit.GetSourceAddress(),
			currentConnLimit.GetDestinationAddress(),
		)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to remove connection limit: %w", err))
		}
	}

	if currentConnRateLimitSig != nextConnRateLimitSig && currentConnRateLimitSig != "" {
		err := h.netMan.DelConnRateLimit(aVip.ToIpNet(), uint16(current.GetPort()), isUdp,
			currentConnRateLimit.RateAsNftables(),
			currentConnRateLimit.GetBurst(),
			currentConnRateLimit.GetSourceAddress(),
			currentConnRateLimit.GetDestinationAddress(),
		)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to remove connection rate limit: %w", err))
		}
	}
	err = h.AddAllLimits(aVip, next, isUdp)
	if err != nil {
		result = multierror.Append(result, fmt.Errorf("failed to add connection limit: %w", err))
	}
	return result
}

func (h *VsHandler) AddAllLimits(aVip *vip.VIP, virtServ *vs.VirtualServer, isUdp bool) error {
	var result error
	confConnLimit := virtServ.GetFirstConnLimit()
	if confConnLimit != nil {
		err := h.netMan.AddConnLimit(aVip.ToIpNet(), uint16(virtServ.GetPort()), isUdp,
			confConnLimit.GetMaxConnections(),
			confConnLimit.GetSourceAddress(),
			confConnLimit.GetDestinationAddress(),
		)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to add connection limit: %w", err))
		}
	}

	confConnRateLimit := virtServ.GetFirstConnRateLimit()
	if confConnRateLimit != nil {
		err := h.netMan.AddConnRateLimit(aVip.ToIpNet(), uint16(virtServ.GetPort()), isUdp,
			confConnRateLimit.RateAsNftables(),
			confConnRateLimit.GetBurst(),
			confConnRateLimit.GetSourceAddress(),
			confConnRateLimit.GetDestinationAddress(),
		)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to add connection rate limit: %w", err))
		}
	}
	return result
}

func (h *VsHandler) addUpdateForVip(ctx context.Context, message *vs.VirtualServer, aVip *vip.VIP) error {
	var err error
	if h.hasMajorDiffWithCache(aVip, message.GetPort(), message) {
		h.entry.Debug().
			Str("virtual_server", message.GetUUID()).
			Str("vip", aVip.GetUUID()).
			Msg("major diff detected, removing route and re-adding it")
		err := h.removeRoute(ctx, h.cacheKey(message, aVip))
		if err != nil {
			return fmt.Errorf("failed to remove route: %w", err)
		}
		return h.addUpdateForVip(ctx, message, aVip)
	}

	currentDataRaw, hasPrevData := h.cachedVss.Load(h.cacheKey(message, aVip))
	cvs := &cachedVs{
		aVip:        aVip,
		virtServ:    message,
		routeConfig: message.RetrieveRouteConfig(),
	}

	_, isUdp := cvs.routeConfig.(*vs.UdpRouteConfig)
	err = h.checkPortNat(cvs, isUdp)
	if err != nil {
		return err
	}

	l3LogOwner := h.otherVsOnPort(cvs, isUdp, enablesL3Log)
	if message.GetEnableL3Log() {
		l3LogOwner = cvs
	}
	err = h.setPortL3Log(cvs, isUdp, l3LogOwner)
	if err != nil {
		return err
	}

	var currentVs *vs.VirtualServer
	// we do nothing as this vip/vs combination already exists and vs has not changed
	if hasPrevData {
		currentData := currentDataRaw.(*cachedVs)
		currentVs = currentData.virtServ
		prevSign, _ := helpers.MessageSignature(currentData.virtServ)
		currentSign, _ := helpers.MessageSignature(message)
		if prevSign == currentSign {
			return nil
		}
	}

	err = h.addUpdateLimits(currentVs, message, aVip, isUdp)
	if err != nil {
		return fmt.Errorf("failed to add or update limits: %w", err)
	}

	msgEntry := h.entry.With().Str("vs_uuid", message.GetUUID()).Logger()
	var statsAddSuccess *prometheus.CounterVec
	var statsAddFailure *prometheus.CounterVec
	typeUrl := ""
	switch cvs.routeConfig.(type) {
	case *vs.TcpRouteConfig:
		statsAddSuccess = stats.addTcpRouteSuccess
		statsAddFailure = stats.addTcpRouteFailure
		typeUrl = helpers.TypeUrl[*vs.TcpRouteConfig]()
		if !hasPrevData {
			msgEntry.Debug().Msg("adding tcp route")
			err = h.tcpRouteHandler.CreateRoute(ctx, aVip, message, message.GetTcpRouteConfig())
		} else {
			msgEntry.Debug().Msg("updating tcp route")
			err = h.tcpRouteHandler.UpdateRoute(ctx, aVip, message, message.GetTcpRouteConfig())
		}
	case *vs.UdpRouteConfig:
		statsAddSuccess = stats.addUdpRouteSuccess
		statsAddFailure = stats.addUdpRouteFailure
		typeUrl = helpers.TypeUrl[*vs.UdpRouteConfig]()
		if !hasPrevData {
			msgEntry.Debug().Msg("adding udp route")
			err = h.udpRouteHandler.CreateRoute(ctx, aVip, message, message.GetUdpRouteConfig())
		} else {
			msgEntry.Debug().Msg("updating udp route")
			err = h.udpRouteHandler.UpdateRoute(ctx, aVip, message, message.GetUdpRouteConfig())
		}
	case *vs.HttpRoutesConfig:
		statsAddSuccess = stats.addHttpRouteSuccess
		statsAddFailure = stats.addHttpRouteFailure
		typeUrl = helpers.TypeUrl[*vs.HttpRoutesConfig]()
		if !hasPrevData {
			msgEntry.Debug().Msg("adding http route")
			err = h.httpRouteHandler.CreateRoute(ctx, aVip, message, message.GetHttpRoutesConfig())
		} else {
			msgEntry.Debug().Msg("updating http route")
			err = h.httpRouteHandler.UpdateRoute(ctx, aVip, message, message.GetHttpRoutesConfig())
		}
		if err == nil && message.GetEnableL3Log() {
			err = h.netMan.AddMarkLog(aVip.ToIpNet(), uint16(message.GetPort()), isUdp)
			if err != nil {
				return fmt.Errorf("failed to add mark log: %w", err)
			}
		}
	case *vs.TlsTcpRoutesConfig:
		statsAddSuccess = stats.addTlsTcpRouteSuccess
		statsAddFailure = stats.addTlsTcpRouteFailure
		typeUrl = helpers.TypeUrl[*vs.TlsTcpRoutesConfig]()
		if !hasPrevData {
			msgEntry.Debug().Msg("adding tls tcp route")
			err = h.tlsTcpRouteHandler.CreateRoute(ctx, aVip, message, message.GetTlsTcpRoutesConfig())
		} else {
			msgEntry.Debug().Msg("updating tls tcp route")
			err = h.tlsTcpRouteHandler.UpdateRoute(ctx, aVip, message, message.GetTlsTcpRoutesConfig())
		}
		if err == nil && message.GetEnableL3Log() {
			err = h.netMan.AddMarkLog(aVip.ToIpNet(), uint16(message.GetPort()), isUdp)
			if err != nil {
				return fmt.Errorf("failed to add mark log: %w", err)
			}
		}
	}
	if err != nil {
		statsAddFailure.WithLabelValues(typeUrl).Inc()
		return fmt.Errorf("failed to create or update route: %w", err)
	}
	statsAddSuccess.WithLabelValues(typeUrl).Inc()
	h.cachedVss.Store(h.cacheKey(message, aVip), cvs)
	return nil
}

func (h *VsHandler) OnMessageRemove(ctx context.Context, message *vs.VirtualServer) error {
	var result error
	h.cachedVss.Range(func(key, value any) bool {
		if !h.isCacheKeyOfVs(key.(string), message.GetUUID()) {
			return true
		}
		vipLock := h.retrieveVipLock(value.(*cachedVs).aVip.GetUUID())
		vipLock.Lock()
		defer vipLock.Unlock()
		err := h.removeRoute(ctx, key.(string))
		if err != nil {
			result = multierror.Append(result,
				fmt.Errorf("failed to remove virtual server %s: %w", message.GetUUID(), err),
			)
		}
		return true
	})
	if result != nil {
		return result
	}
	h.loadedVs.Delete(message.GetUUID())
	return nil
}

func (h *VsHandler) hasMajorDiffWithCache(aVip *vip.VIP, port uint32, message *vs.VirtualServer) bool {
	cachedVsRaw, ok := h.cachedVss.Load(h.cacheKey(message, aVip))
	if !ok {
		return false
	}
	cachedVs := cachedVsRaw.(*cachedVs)
	if cachedVs.virtServ.Port != port {
		return true
	}
	if cachedVs.aVip.GetIp() != aVip.GetIp() {
		return true
	}
	if cachedVs.aVip.GetPrefixLength() != aVip.GetPrefixLength() {
		return true
	}
	hasDiff := false
	switch cachedVs.routeConfig.(type) {
	case *vs.TcpRouteConfig:
		hasDiff = message.GetUdpRouteConfig() != nil || message.GetHttpRoutesConfig() != nil || message.GetTlsTcpRoutesConfig() != nil
	case *vs.UdpRouteConfig:
		hasDiff = message.GetTcpRouteConfig() != nil || message.GetHttpRoutesConfig() != nil || message.GetTlsTcpRoutesConfig() != nil
	case *vs.HttpRoutesConfig:
		hasDiff = message.GetTcpRouteConfig() != nil || message.GetUdpRouteConfig() != nil || message.GetTlsTcpRoutesConfig() != nil
	case *vs.TlsTcpRoutesConfig:
		hasDiff = message.GetTcpRouteConfig() != nil || message.GetUdpRouteConfig() != nil || message.GetHttpRoutesConfig() != nil
	}
	return hasDiff
}

func (h *VsHandler) deleteAllLimits(aVip *vip.VIP, virtServ *vs.VirtualServer, isUdp bool) {
	confConnLimit := virtServ.GetFirstConnLimit()
	if confConnLimit != nil {
		h.netMan.DelConnLimit(aVip.ToIpNet(), uint16(virtServ.GetPort()), isUdp, // nolint:errcheck
			confConnLimit.GetMaxConnections(),
			confConnLimit.GetSourceAddress(),
			confConnLimit.GetDestinationAddress(),
		)
	}

	confConnRateLimit := virtServ.GetFirstConnRateLimit()
	if confConnRateLimit != nil {
		h.netMan.DelConnRateLimit(aVip.ToIpNet(), uint16(virtServ.GetPort()), isUdp, // nolint:errcheck
			confConnRateLimit.RateAsNftables(),
			confConnRateLimit.GetBurst(),
			confConnRateLimit.GetSourceAddress(),
			confConnRateLimit.GetDestinationAddress(),
		) // nolint:errcheck
	}
}

func anyVs(*cachedVs) bool {
	return true
}

func enablesL3Log(other *cachedVs) bool {
	return other.virtServ.GetEnableL3Log()
}

// otherVsOnPort gives another virtual server cached on the vip ip, port and protocol of c that matches, nil when none.
func (h *VsHandler) otherVsOnPort(c *cachedVs, isUdp bool, matches func(*cachedVs) bool) *cachedVs {
	var found *cachedVs
	h.cachedVss.Range(func(_, value any) bool {
		other := value.(*cachedVs)
		_, isOtherUdp := other.routeConfig.(*vs.UdpRouteConfig)
		isOtherVs := other.virtServ.GetUUID() != c.virtServ.GetUUID()
		isSameAddress := other.aVip.GetIp() == c.aVip.GetIp() && other.virtServ.GetPort() == c.virtServ.GetPort()
		if isOtherVs && isSameAddress && isOtherUdp == isUdp && matches(other) {
			found = other
		}
		return found == nil
	})
	return found
}

// setPortL3Log keeps the l3 log of the port of c on while owner, one of its virtual servers, enables it. Packets are
// logged before the SNI tells the virtual servers of a shared port apart, so they are logged under the uuid of owner.
func (h *VsHandler) setPortL3Log(c *cachedVs, isUdp bool, owner *cachedVs) error {
	ipNet := c.aVip.ToIpNet()
	port := uint16(c.virtServ.GetPort())
	if owner == nil {
		h.netMan.DelNfLog(ipNet, port, c.virtServ.GetUUID(), isUdp) // nolint:errcheck
		h.netMan.DelMarkLog(ipNet, port, isUdp)                     // nolint:errcheck
		return nil
	}
	err := h.netMan.AddNfLog(ipNet, port, owner.virtServ.GetUUID(), isUdp)
	if err != nil {
		return fmt.Errorf("failed to add nflog: %w", err)
	}
	return nil
}

// checkPortNat refuses a virtual server whose nat differs from another one of its port: envoy applies disable_snat
// and disable_dnat with the listener filters of the port, which its virtual servers share.
func (h *VsHandler) checkPortNat(c *cachedVs, isUdp bool) error {
	other := h.otherVsOnPort(c, isUdp, anyVs)
	if other == nil {
		return nil
	}
	nat, err := natOf(c.virtServ)
	if err != nil {
		return err
	}
	otherNat, err := natOf(other.virtServ)
	if err != nil {
		return err
	}
	if nat != otherNat {
		return fmt.Errorf("disable_snat and disable_dnat must be the same as on virtual server %s, which shares port %d "+
			"of vip %s: they apply to the whole port", other.virtServ.GetUUID(), c.virtServ.GetPort(), c.aVip.GetUUID())
	}
	return nil
}

type portNat struct {
	disableSnat bool
	disableDnat bool
}

func natOf(virtServ *vs.VirtualServer) (portNat, error) {
	if httpConfig := virtServ.GetHttpRoutesConfig(); httpConfig != nil {
		opts, err := httpConfig.GetStarlingOptions()
		if err != nil {
			return portNat{}, fmt.Errorf("failed to retrieve agent options: %w", err)
		}
		return portNat{disableSnat: opts.GetDisableSnat(), disableDnat: opts.GetDisableDnat()}, nil
	}
	if tlsConfig := virtServ.GetTlsTcpRoutesConfig(); tlsConfig != nil {
		opts, err := tlsConfig.GetStarlingOptions()
		if err != nil {
			return portNat{}, fmt.Errorf("failed to retrieve agent options: %w", err)
		}
		return portNat{disableSnat: opts.GetDisableSnat(), disableDnat: opts.GetDisableDnat()}, nil
	}
	// tcp and udp virtual servers never share a port, the controller refuses it
	return portNat{}, nil
}

func (h *VsHandler) removeRoute(ctx context.Context, cacheKey string) error {
	cachedVsRaw, ok := h.cachedVss.Load(cacheKey)
	if !ok {
		return nil
	}
	cachedVs := cachedVsRaw.(*cachedVs)
	typeUrl := ""
	var statsRmSuccess *prometheus.CounterVec
	var statsRmFailure *prometheus.CounterVec
	var err error

	_, isUdp := cachedVs.routeConfig.(*vs.UdpRouteConfig)

	h.setPortL3Log(cachedVs, isUdp, h.otherVsOnPort(cachedVs, isUdp, enablesL3Log)) // nolint:errcheck // no need to check error here, since we are removing the route anyway
	// the limits of a port belong to every virtual server on it, the last one to leave removes them
	if h.otherVsOnPort(cachedVs, isUdp, anyVs) == nil {
		h.deleteAllLimits(cachedVs.aVip, cachedVs.virtServ, isUdp)
	}

	switch cachedVs.routeConfig.(type) {
	case *vs.TcpRouteConfig:
		typeUrl = helpers.TypeUrl[*vs.TcpRouteConfig]()
		statsRmSuccess = stats.rmTcpRouteSuccess
		statsRmFailure = stats.rmTcpRouteFailure
		err = h.tcpRouteHandler.DeleteRoute(ctx, cachedVs.aVip, cachedVs.virtServ, cachedVs.routeConfig.(*vs.TcpRouteConfig))
	case *vs.UdpRouteConfig:
		typeUrl = helpers.TypeUrl[*vs.UdpRouteConfig]()
		statsRmSuccess = stats.rmUdpRouteSuccess
		statsRmFailure = stats.rmUdpRouteFailure
		err = h.udpRouteHandler.DeleteRoute(ctx, cachedVs.aVip, cachedVs.virtServ, cachedVs.routeConfig.(*vs.UdpRouteConfig))
	case *vs.HttpRoutesConfig:
		typeUrl = helpers.TypeUrl[*vs.HttpRoutesConfig]()
		statsRmSuccess = stats.rmHttpRouteSuccess
		statsRmFailure = stats.rmHttpRouteFailure
		err = h.httpRouteHandler.DeleteRoute(ctx, cachedVs.aVip, cachedVs.virtServ, cachedVs.routeConfig.(*vs.HttpRoutesConfig))
	case *vs.TlsTcpRoutesConfig:
		typeUrl = helpers.TypeUrl[*vs.TlsTcpRoutesConfig]()
		statsRmSuccess = stats.rmTlsTcpRouteSuccess
		statsRmFailure = stats.rmTlsTcpRouteFailure
		err = h.tlsTcpRouteHandler.DeleteRoute(ctx, cachedVs.aVip, cachedVs.virtServ, cachedVs.routeConfig.(*vs.TlsTcpRoutesConfig))
	}
	if err != nil {
		statsRmFailure.WithLabelValues(typeUrl).Inc()
		return fmt.Errorf("failed to delete route: %w", err)
	}
	statsRmSuccess.WithLabelValues(typeUrl).Inc()
	h.cachedVss.Delete(cacheKey)
	return nil
}

func (h *VsHandler) Clean() error {
	h.entry.Debug().Msg("Cleaning virtual server")
	defer h.entry.Debug().Msg("Finished Cleaning virtual server")
	var result error
	if cleanableHandler, ok := h.tcpRouteHandler.(adshandlers.Cleanable); ok {
		err := cleanableHandler.Clean()
		if err != nil {
			result = multierror.Append(result, err)
		}
	}
	if cleanableHandler, ok := h.udpRouteHandler.(adshandlers.Cleanable); ok {
		err := cleanableHandler.Clean()
		if err != nil {
			result = multierror.Append(result, err)
		}
	}
	if cleanableHandler, ok := h.httpRouteHandler.(adshandlers.Cleanable); ok {
		err := cleanableHandler.Clean()
		if err != nil {
			result = multierror.Append(result, err)
		}
	}
	if cleanableHandler, ok := h.tlsTcpRouteHandler.(adshandlers.Cleanable); ok {
		err := cleanableHandler.Clean()
		if err != nil {
			result = multierror.Append(result, err)
		}
	}
	return result
}

func (h *VsHandler) AttachVip(ctx context.Context, aVip *vip.VIP, vsUUID string) error {
	h.retrieveVsLoadingLock(vsUUID).RLock()
	defer h.retrieveVsLoadingLock(vsUUID).RUnlock()

	var currentVS *vs.VirtualServer
	h.cachedVss.Range(func(key, value any) bool {
		if !h.isCacheKeyOfVs(key.(string), vsUUID) {
			return true
		}
		currentVS = value.(*cachedVs).virtServ
		return false
	})
	_, wasLoaded := h.loadedVs.Load(vsUUID)
	// if nil, then the virtual server is not found in the cache
	// and was not loaded by ads then it should be ignore until loaded
	if currentVS == nil && !wasLoaded {
		return nil
	}
	if currentVS == nil {
		// virtual server was loaded but all its vips were detached, so it is not in cache anymore
		var err error
		currentVS, err = h.store.VirtualServers().Read(ctx, vsUUID)
		if err != nil {
			return fmt.Errorf("failed to retrieve virtual server %s: %w", vsUUID, err)
		}
	}
	// lock on the same key as OnMessageAdded to avoid attach/detach during virtual server update
	h.retrieveVipLock(aVip.GetUUID()).Lock()
	defer h.retrieveVipLock(aVip.GetUUID()).Unlock()

	_, exists := h.cachedVss.Load(h.cacheKey(currentVS, aVip))
	// if exists, then the virtual server is already attached to the vip
	// we do it after lock because it was maybe lock by on message added
	if exists {
		return nil
	}
	h.entry.Debug().Str("vip", aVip.GetUUID()).Str("virtual_server", vsUUID).Msg("Attaching vip to virtual server")
	return h.addUpdateForVip(ctx, currentVS, aVip)
}

func (h *VsHandler) DetachVip(ctx context.Context, aVip *vip.VIP, vsUUID string) error {
	h.retrieveVsLoadingLock(vsUUID).RLock()
	defer h.retrieveVsLoadingLock(vsUUID).RUnlock()

	h.retrieveVipLock(aVip.GetUUID()).Lock()
	defer h.retrieveVipLock(aVip.GetUUID()).Unlock()

	cacheKey := h.cacheKeyRaw(vsUUID, aVip.GetUUID())
	_, exists := h.cachedVss.Load(cacheKey)
	if !exists {
		return nil
	}
	h.entry.Debug().Str("vip", aVip.GetUUID()).Str("virtual_server", vsUUID).Msg("Detaching vip to virtual server")
	return h.removeRoute(ctx, cacheKey)
}

func (h *VsHandler) Name() string {
	return "virtual_server"
}
