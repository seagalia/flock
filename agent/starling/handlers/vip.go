package handlers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog"
	"github.com/samber/lo"
	"github.com/seagalia/flock/agent/starling/nm"
	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/storers"
)

const (
	cleanTimeout = 10 * time.Second
)

type VipHandler struct {
	entry     zerolog.Logger
	netMan    nm.NetworkManager
	cachedVip *sync.Map
	adHandler AttachDetachHandler
	vips      storers.Storer[*vip.VIP]
}

// NewVipHandler removes the vips of vips from the network on clean.
func NewVipHandler(
	netMan nm.NetworkManager, adHandler AttachDetachHandler, vips storers.Storer[*vip.VIP],
) adshandlers.MessageHandler[*vip.VIP] {
	return &VipHandler{
		vips:      vips,
		entry:     logging.Component("vip_handler"),
		netMan:    netMan,
		cachedVip: &sync.Map{},
		adHandler: adHandler,
	}
}

func (h *VipHandler) OnMessageAdded(ctx context.Context, message *vip.VIP) error {
	prevVipRaw, shouldUpdate := h.cachedVip.Load(message.GetUUID())
	// a wildcard vip stands for addresses already on the interfaces of the machine, it is never added
	if message.IsWildcard() {
		var prevVip *vip.VIP
		if shouldUpdate {
			prevVip = prevVipRaw.(*vip.VIP)
		}
		h.warnWildcardAnnounce(prevVip, message)
	} else {
		err := h.netMan.AddVip(message.ToIpNet())
		if err != nil {
			return err
		}
	}
	if !shouldUpdate {
		var result error
		for _, vs := range message.GetVirtualServersUuid() {
			err := h.adHandler.AttachVip(ctx, message, vs)
			if err != nil {
				result = multierror.Append(result, err)
			}
		}
		if result != nil {
			return result
		}
		h.cachedVip.Store(message.GetUUID(), message)
		return nil
	}
	prevVip := prevVipRaw.(*vip.VIP)
	toAttach, toDetach := lo.Difference[string](message.GetVirtualServersUuid(), prevVip.GetVirtualServersUuid())

	var result error
	for _, vs := range toDetach {
		err := h.adHandler.DetachVip(ctx, message, vs)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to detach vip %s to virtual server %s", message.GetUUID(), vs))
		}
	}

	for _, vs := range toAttach {
		err := h.adHandler.AttachVip(ctx, message, vs)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to atach vip %s to virtual server %s", message.GetUUID(), vs))
		}
	}

	if result != nil {
		return result
	}

	h.cachedVip.Store(message.GetUUID(), message)
	return nil
}

func (h *VipHandler) OnMessageRemove(ctx context.Context, message *vip.VIP) error {
	if !message.IsWildcard() {
		err := h.netMan.DelVip(message.ToIpNet())
		if err != nil {
			return err
		}
	}

	for _, vs := range message.GetVirtualServersUuid() {
		err := h.adHandler.DetachVip(ctx, message, vs)
		if err != nil {
			return err
		}
	}

	h.cachedVip.Delete(message.GetUUID())
	return nil
}

// Clean is called once the ctx of the handler is done, so its read of the vips runs on a ctx of its own
func (h *VipHandler) Clean() error {
	h.entry.Debug().Msg("cleaning vip handler ...")
	ctx, cancel := context.WithTimeout(context.Background(), cleanTimeout)
	defer cancel()
	vips, err := h.vips.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to list vips: %w", err)
	}
	var result error
	for _, aVip := range vips {
		if aVip.IsWildcard() {
			continue
		}
		if err := h.netMan.DelVip(aVip.ToIpNet()); err != nil {
			result = multierror.Append(result, err)
		}
	}
	h.entry.Debug().Msg("vip handler cleaned")
	return result
}

// warnWildcardAnnounce warns once per announce asked, since a wildcard vip is always static and the layer2 and bgp
// handlers skip it
func (h *VipHandler) warnWildcardAnnounce(prevVip, aVip *vip.VIP) {
	announce, _ := aVip.GetStarlingAnnounce()
	typeAnnounce := announce.GetTypeAnnounce()
	// with no typed_announce the BGP default is no choice of the user
	asksNothing := aVip.GetTypedAnnounce() == nil || typeAnnounce == starling_announce.StarlingAnnounce_STATIC
	if asksNothing {
		return
	}
	if prevVip != nil {
		prevAnnounce, _ := prevVip.GetStarlingAnnounce()
		if prevAnnounce.GetTypeAnnounce() == typeAnnounce {
			return
		}
	}
	h.entry.Warn().Str("vip", aVip.GetUUID()).Stringer("type_announce", typeAnnounce).
		Msg("a wildcard vip is never announced, it is served as static")
}

func (h *VipHandler) Name() string {
	return "vip"
}
