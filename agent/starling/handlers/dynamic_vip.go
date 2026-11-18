package handlers

import (
	"context"
	"fmt"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/agent/starling/agentlist"
	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/storers"
)

type DynamicPlacementVipHandler struct {
	entry      zerolog.Logger
	vipHandler adshandlers.MessageHandler[*vip.VIP]
	placer     *agentlist.VipPlacer
	vips       storers.Storer[*vip.VIP]
}

// NewDynamicPlacementVipHandler runs vipHandler on the vips the placer gives this agent, among the ones of vips.
func NewDynamicPlacementVipHandler(
	placer *agentlist.VipPlacer, vipHandler adshandlers.MessageHandler[*vip.VIP], vips storers.Storer[*vip.VIP],
) *DynamicPlacementVipHandler {
	h := &DynamicPlacementVipHandler{
		entry:      logging.Component("dynamic_placement_handler"),
		vipHandler: vipHandler,
		placer:     placer,
		vips:       vips,
	}
	if placer != nil {
		placer.AddPlacerListener(h)
	}
	return h
}

func (h *DynamicPlacementVipHandler) OnPlacerAddedVip(ctx context.Context, aVip *vip.VIP) error {
	return h.vipHandler.OnMessageAdded(context.Background(), aVip)
}

func (h *DynamicPlacementVipHandler) OnAfterCalculate(ctx context.Context, vips []*vip.VIP) error {
	return nil
}

func (h *DynamicPlacementVipHandler) OnPlacerRemoveVip(ctx context.Context, aVip *vip.VIP) error {
	return h.vipHandler.OnMessageRemove(ctx, aVip)
}

func (h *DynamicPlacementVipHandler) OnMessageAdded(ctx context.Context, aVip *vip.VIP) error {
	return h.placer.RecalculateAndApply(ctx)
}

func (h *DynamicPlacementVipHandler) OnMessageRemove(ctx context.Context, aVip *vip.VIP) error {
	// we delete manually to ensure this is not recalculated
	err := h.vips.Delete(ctx, aVip.GetUUID())
	if err != nil {
		return fmt.Errorf("unable to delete vip: %s", err.Error())
	}
	err = h.placer.RecalculateAndApply(ctx)
	if err != nil {
		// rollback vip deletion
		h.vips.Create(ctx, aVip) // nolint:errcheck
		return fmt.Errorf("unable to recalculate and apply: %s", err.Error())
	}
	return nil
}

func (h *DynamicPlacementVipHandler) Stop() error {
	if h.placer != nil {
		h.placer.RemovePlacerListener(h)
	}
	return nil
}

func (h *DynamicPlacementVipHandler) Name() string {
	return "dynamic_placement_for_" + h.vipHandler.Name()
}
