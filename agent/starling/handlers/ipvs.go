package handlers

import (
	"fmt"
	"sync"

	"github.com/cloudflare/ipvs"
	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog"
	"github.com/samber/lo"
	"github.com/seagalia/flock/agent/starling/ipvsctl"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	starling_backendopts "github.com/seagalia/flock/sdk/flock/extensions/backend/starling_options/v1"
	"github.com/seagalia/flock/sdk/logging"
)

type cacheVipOpt struct {
	ip  *core.IPAddr
	opt *starling_backendopts.BackendOptions
}

type IpvsHandler struct {
	entry     zerolog.Logger
	service   *ipvs.Service
	mutex     *sync.Mutex
	cachedIps map[string]*cacheVipOpt
	fwdType   ipvs.ForwardType
}

func NewIpvsHandler(service *ipvs.Service, fwdType ipvs.ForwardType) *IpvsHandler {
	cachedIps := make(map[string]*cacheVipOpt)
	curDests, err := ipvsctl.Destinations(*service)
	if err == nil {
		for _, dest := range curDests {
			ipAddr := &core.IPAddr{
				Ip:                  dest.Address.Unmap().String(),
				Port:                uint32(dest.Port),
				LoadBalancingWeight: dest.Weight,
			}
			cachedIps[ipAddr.Format()] = &cacheVipOpt{
				ip:  ipAddr,
				opt: &starling_backendopts.BackendOptions{},
			}
		}
	}

	return &IpvsHandler{
		entry: logging.Component("ipvs_handler").
			With().
			Str("service", ipvsctl.ServiceToIpNet(*service).String()).
			Uint16("port", service.Port).
			Logger(),
		service:   service,
		mutex:     &sync.Mutex{},
		cachedIps: cachedIps,
		fwdType:   fwdType,
	}
}

func (h *IpvsHandler) OnStartup(backendDef *backend.Backend, ipAddresses []*core.IPAddr) {
	if len(ipAddresses) == 0 {
		err := h.Clean()
		if err != nil {
			h.entry.Error().Err(err).Msg("failed to clean ipvs handler on startup")
		}
		return
	}

	opts, err := backendDef.GetStarlingOptions()
	if err != nil {
		h.entry.Error().Err(err).Msg("failed to get agent options")
	}

	cachedVips := make([]*cacheVipOpt, 0)
	for _, ipAddr := range ipAddresses {
		cachedVips = append(cachedVips, &cacheVipOpt{
			ip:  ipAddr,
			opt: opts,
		})
	}
	h.mutex.Lock()
	defer h.mutex.Unlock()
	err = h.updateDiff(cachedVips)
	if err != nil {
		h.entry.Error().Err(err).Msg("failed to update ipvs handler on startup")
	}
}

func (h *IpvsHandler) ipAddrToDest(cachedVip *cacheVipOpt) (*ipvs.Destination, error) {
	ipNet := cachedVip.ip.ToIpNet()
	if ipNet == nil {
		return nil, fmt.Errorf("invalid ip address %s", cachedVip.ip.Format())
	}
	family := ipvs.INET
	if cachedVip.ip.IsIPV6() {
		family = ipvs.INET6
	}
	return &ipvs.Destination{
		Address:   ipvsctl.AddrFromIP(ipNet.IP),
		FwdMethod: h.fwdType,
		// +1 as a weight of 0 makes a quiescent server: it receives no new connections
		Weight: cachedVip.ip.GetLoadBalancingWeight() + 1,
		Port:   uint16(cachedVip.ip.GetPort()),
		Family: family,
	}, nil
}

func (h *IpvsHandler) updateDiff(newIpAddresses []*cacheVipOpt) error {
	toRemove := make([]*cacheVipOpt, 0)
	for key, cachedIp := range h.cachedIps {
		if !lo.ContainsBy[*cacheVipOpt](newIpAddresses, func(item *cacheVipOpt) bool {
			return item.ip.Format() == key
		}) {
			toRemove = append(toRemove, cachedIp)
		}
	}
	var result error
	for _, ipAddr := range newIpAddresses {
		err := h.addOrUpdateDestination(ipAddr)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to add or update destination: %w", err))
		}
	}
	for _, ipAddr := range toRemove {
		err := h.removeDestination(ipAddr)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to remove destination: %w", err))
		}
	}
	return result
}

func (h *IpvsHandler) addOrUpdateDestination(cachedVip *cacheVipOpt) error {
	dest, err := h.ipAddrToDest(cachedVip)
	if err != nil {
		return fmt.Errorf("failed to convert ip address to destination: %w", err)
	}
	_, toUpdate := h.cachedIps[cachedVip.ip.Format()]
	if toUpdate {
		err := ipvsctl.UpdateDestination(*h.service, *dest)
		if err != nil {
			return fmt.Errorf("failed to update destination: %w", err)
		}
	} else {
		err := ipvsctl.CreateDestination(*h.service, *dest)
		if err != nil {
			return fmt.Errorf("failed to create destination: %w", err)
		}
	}
	h.cachedIps[cachedVip.ip.Format()] = cachedVip
	return nil
}

func (h *IpvsHandler) removeDestination(cachedVip *cacheVipOpt) error {
	dest, err := h.ipAddrToDest(cachedVip)
	if err != nil {
		return fmt.Errorf("failed to convert ip address to destination: %w", err)
	}
	err = ipvsctl.RemoveDestination(*h.service, *dest)
	if err != nil {
		return fmt.Errorf("failed to remove destination: %w", err)
	}
	delete(h.cachedIps, cachedVip.ip.Format())
	return nil
}

func (h *IpvsHandler) SetForwardType(fwdType ipvs.ForwardType) error {
	if fwdType == h.fwdType {
		return nil
	}
	h.mutex.Lock()
	defer h.mutex.Unlock()
	for _, cachedVip := range h.cachedIps {
		dest, err := h.ipAddrToDest(cachedVip)
		if err != nil {
			return fmt.Errorf("failed to convert ip address to destination: %w", err)
		}
		err = ipvsctl.RemoveDestination(*h.service, *dest)
		if err != nil {
			return fmt.Errorf("failed to delete destination: %w", err)
		}
	}
	h.fwdType = fwdType
	for _, cachedVip := range h.cachedIps {
		dest, err := h.ipAddrToDest(cachedVip)
		if err != nil {
			return fmt.Errorf("failed to convert ip address to destination: %w", err)
		}
		err = ipvsctl.CreateDestination(*h.service, *dest)
		if err != nil {
			return fmt.Errorf("failed to recreate destination: %w", err)
		}
	}
	return nil
}

func (h *IpvsHandler) OnEndpointAdded(backendDef *backend.Backend, ipAddr *core.IPAddr) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	opts, err := backendDef.GetStarlingOptions()
	if err != nil {
		h.entry.Error().Err(err).Msg("failed to get agent options")
	}
	err = h.addOrUpdateDestination(&cacheVipOpt{
		ip:  ipAddr,
		opt: opts,
	})
	if err != nil {
		h.entry.Error().Err(err).Msgf("failed to add or update destination %s", ipAddr.Format())
	}
}

func (h *IpvsHandler) OnEndpointRemoved(backendDef *backend.Backend, ipAddr *core.IPAddr) {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	opts, err := backendDef.GetStarlingOptions()
	if err != nil {
		h.entry.Error().Err(err).Msg("failed to get agent options")
	}
	err = h.removeDestination(&cacheVipOpt{
		ip:  ipAddr,
		opt: opts,
	})
	if err != nil {
		h.entry.Error().Err(err).Msgf("failed to remove destination %s", ipAddr.Format())
	}
}

func (h *IpvsHandler) Clean() error {
	h.mutex.Lock()
	defer h.mutex.Unlock()
	var result error
	for _, ipAddr := range h.cachedIps {
		err := h.removeDestination(ipAddr)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to remove destination: %w", err))
		}
	}
	return result
}

func (h *IpvsHandler) Name() string {
	return "ipvs"
}
