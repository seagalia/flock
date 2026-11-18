package agentlist

import (
	"context"
	"fmt"
	"sync"

	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/memberlist"
	"github.com/rs/zerolog"
	"github.com/samber/lo"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/storers"
)

type VipPlacerListener interface {
	OnPlacerAddedVip(ctx context.Context, aVip *vip.VIP) error
	OnPlacerRemoveVip(ctx context.Context, aVip *vip.VIP) error
	OnAfterCalculate(ctx context.Context, vips []*vip.VIP) error
}

type VipPlacer struct {
	// ctx is the agent's: memberlist reports node events with no ctx, the placement they trigger runs on it
	ctx           context.Context
	entry         zerolog.Logger
	minlist       *AgentList
	calcMutex     *sync.Mutex
	storeMutex    *sync.RWMutex
	nodeName      string
	announcedVips []*vip.VIP
	listeners     *sync.Map
	placerStrat   PlacerStrat
	placeOn       []starling_announce.StarlingAnnounce_TypeAnnounce
	vips          storers.Storer[*vip.VIP]
}

// NewVipPlacer places the vips of the vips store among the agents of minlist.
func NewVipPlacer(ctx context.Context, nodeName string, minlist *AgentList, placerStrat PlacerStrat, vips storers.Storer[*vip.VIP]) *VipPlacer {
	if placerStrat == nil {
		placerStrat, _ = GetPlacerStrat(DefaultPlacerStrat)
	}

	h := &VipPlacer{
		ctx: ctx,
		entry: logging.Component("vip_placer").
			With().
			Str("placing_strategy", placerStrat.Name()).
			Logger(),
		minlist:     minlist,
		calcMutex:   &sync.Mutex{},
		storeMutex:  &sync.RWMutex{},
		nodeName:    nodeName,
		listeners:   &sync.Map{},
		placerStrat: placerStrat,
		placeOn:     []starling_announce.StarlingAnnounce_TypeAnnounce{},
		vips:        vips,
	}
	if minlist != nil {
		h.entry.Debug().Msg("add node event listener")
		minlist.AddNodeEventListener(h)
	}
	return h
}

func (h *VipPlacer) RecalculateAndApply(ctx context.Context) error {
	return h.calculateAndApply(ctx)
}

func (h *VipPlacer) OnNodeJoin(n *memberlist.Node) {
	h.loggedCalculateAndApply(h.ctx)
}

func (h *VipPlacer) OnNodeLeave(*memberlist.Node) {
	h.loggedCalculateAndApply(h.ctx)
}

func (h *VipPlacer) OnNodeUpdate(n *memberlist.Node) {
	h.entry.Debug().
		Str("node_name", n.Name).
		Stringer("node_addr", n.Addr).
		Str("meta", string(n.Meta)).
		Msg("node update event received")
	h.loggedCalculateAndApply(h.ctx)
}

func (h *VipPlacer) loggedCalculateAndApply(ctx context.Context) {
	err := h.calculateAndApply(ctx)
	if err != nil {
		h.entry.Error().Msgf("failed to calculate and apply: %s", err.Error())
	}
}

func (h *VipPlacer) calculateAndApply(ctx context.Context) error {
	h.calcMutex.Lock()
	defer h.calcMutex.Unlock()

	// the vips stay placed as they are while the agent stops, its announcers withdraw them
	isLeaving := h.minlist != nil && h.minlist.Leaving()
	if isLeaving {
		h.entry.Debug().Msg("agent list leaving, vip placement kept")
		return nil
	}
	h.entry.Debug().Msg("calculating and applying vip registrations")

	currentVips, err := h.calculate(ctx)
	if err != nil {
		return err
	}
	toAdd := make([]*vip.VIP, 0)
	toRemove := make([]*vip.VIP, 0)
	for _, aVip := range currentVips {
		if vip.VIPs(h.announcedVips).ContainsByIpNet(aVip) {
			continue
		}
		toAdd = append(toAdd, aVip)
	}

	for _, aVip := range h.announcedVips {
		if vip.VIPs(currentVips).ContainsByIpNet(aVip) {
			continue
		}
		toRemove = append(toRemove, aVip)
	}
	h.storeMutex.Lock()
	h.announcedVips = currentVips
	h.listeners.Range(func(key, value any) bool {
		err := value.(VipPlacerListener).OnAfterCalculate(ctx, currentVips)
		if err != nil {
			h.entry.Error().Msgf("failed to call OnAfterCalculate: %s", err.Error())
		}
		return true
	})
	h.storeMutex.Unlock()

	var result error
	for _, aVip := range toAdd {
		h.listeners.Range(func(key, value any) bool {
			err := value.(VipPlacerListener).OnPlacerAddedVip(ctx, aVip)
			if err != nil {
				result = multierror.Append(
					result,
					fmt.Errorf("failed to add vip %s: %s", aVip.ToIpNet().String(), err.Error()),
				)
			}
			return true
		})
	}

	for _, aVip := range toRemove {
		h.listeners.Range(func(key, value any) bool {
			err := value.(VipPlacerListener).OnPlacerRemoveVip(ctx, aVip)
			if err != nil {
				result = multierror.Append(
					result,
					fmt.Errorf("failed to remove vip %s: %s", aVip.ToIpNet().String(), err.Error()),
				)
			}
			return true
		})
	}
	h.entry.Debug().Msg("calculating and applying vip registrations done")
	return result
}

func (h *VipPlacer) calculate(ctx context.Context) ([]*vip.VIP, error) {
	vips, err := h.vips.List(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list vips: %w", err)
	}

	vips = h.filterVips(vips)

	mapMemberVips := make(map[string][]*vip.VIP)
	mapMemberVips[h.nodeName] = make([]*vip.VIP, 0)
	if h.minlist != nil {
		for _, node := range h.minlist.Members() {
			mapMemberVips[node.Name] = make([]*vip.VIP, 0)
		}
	}
	if len(mapMemberVips) == 1 {
		return vips, nil
	}
	for _, aVip := range vips {
		h.placerStrat.Placing(h.minlist.Members(), mapMemberVips, aVip)
	}
	return mapMemberVips[h.nodeName], nil
}

func (h *VipPlacer) filterVips(vips []*vip.VIP) []*vip.VIP {
	var result []*vip.VIP
	for _, aVip := range vips {
		annon, err := aVip.GetStarlingAnnounce()
		if err != nil {
			h.entry.Error().Msgf("failed to get agent announce: %s", err.Error())
		}
		if !lo.ContainsBy[starling_announce.StarlingAnnounce_TypeAnnounce](h.placeOn, func(item starling_announce.StarlingAnnounce_TypeAnnounce) bool {
			return annon.GetTypeAnnounce() == item
		}) {
			continue
		}
		result = append(result, aVip)
	}
	return result
}

func (h *VipPlacer) AddPlaceOn(typeAnnon starling_announce.StarlingAnnounce_TypeAnnounce) {
	h.placeOn = append(h.placeOn, typeAnnon)
}

func (h *VipPlacer) ListPlaceOn() []starling_announce.StarlingAnnounce_TypeAnnounce {
	return h.placeOn
}

func (h *VipPlacer) AddPlacerListener(listener VipPlacerListener) {
	h.listeners.Store(fmt.Sprintf("%p", listener), listener)
}

func (h *VipPlacer) RemovePlacerListener(listener VipPlacerListener) {
	h.listeners.Delete(fmt.Sprintf("%p", listener))
}

func (h *VipPlacer) AnnouncedVips() []*vip.VIP {
	h.storeMutex.RLock()
	defer h.storeMutex.RUnlock()
	return h.announcedVips
}

func (h *VipPlacer) Stop() error {
	if h.minlist != nil {
		h.minlist.RemoveNodeEventListener(h)
	}
	return nil
}
