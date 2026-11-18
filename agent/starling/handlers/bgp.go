package handlers

import (
	"context"

	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
)

type BgpHandler struct {
	bgpReg BgpRegisterer
}

func NewBgpHandler(bgpRegister BgpRegisterer) *BgpHandler {
	return &BgpHandler{
		bgpReg: bgpRegister,
	}
}

func (h *BgpHandler) OnMessageAdded(ctx context.Context, aVip *vip.VIP) error {
	minAnnoun, _ := aVip.GetStarlingAnnounce()
	// a wildcard vip is always static, the vip handler warns when it asks for more
	if minAnnoun.GetTypeAnnounce() != starling_announce.StarlingAnnounce_BGP || aVip.IsWildcard() {
		return nil
	}
	return h.bgpReg.AddVip(ctx, aVip)
}

func (h *BgpHandler) OnMessageRemove(ctx context.Context, aVip *vip.VIP) error {
	minAnnoun, _ := aVip.GetStarlingAnnounce()
	if minAnnoun.GetTypeAnnounce() != starling_announce.StarlingAnnounce_BGP || aVip.IsWildcard() {
		return nil
	}
	return h.bgpReg.DelVip(ctx, aVip)
}

func (h *BgpHandler) Name() string {
	return "bgp"
}
