package handlers

import (
	"context"
	"fmt"
	"net/netip"
	"sync"

	"github.com/rs/zerolog"
	"github.com/seagalia/flock/agent/starling/layer2"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/logging"
)

type Layer2Handler struct {
	entry         zerolog.Logger
	l2Announcer   layer2.Announcer
	mutex         *sync.Mutex
	ifaces        []string
	nodeName      string
	allInterfaces bool
}

func NewLayer2Handler(nodeName string, ifaces []string, allInterfaces bool, l2Announcer layer2.Announcer) *Layer2Handler {
	h := &Layer2Handler{
		entry:         logging.Component("layer2_handler"),
		l2Announcer:   l2Announcer,
		mutex:         &sync.Mutex{},
		ifaces:        ifaces,
		nodeName:      nodeName,
		allInterfaces: allInterfaces,
	}
	return h
}

func (h *Layer2Handler) OnMessageAdded(ctx context.Context, aVip *vip.VIP) error {
	minAnnoun, _ := aVip.GetStarlingAnnounce()
	// a wildcard vip is always static, the vip handler warns when it asks for more
	if minAnnoun.GetTypeAnnounce() != starling_announce.StarlingAnnounce_LAYER2 || aVip.IsWildcard() {
		return nil
	}
	ipNet := aVip.ToIpNet()
	ipAddr, ok := netip.AddrFromSlice(ipNet.IP)
	if !ok {
		return fmt.Errorf("failed to convert ip %s to netip.Addr", ipNet.IP)
	}
	h.l2Announcer.SetBalancer(ipNet.String(), layer2.NewIPAdvertisement(ipAddr, h.allInterfaces, h.ifaces))
	return nil
}

func (h *Layer2Handler) OnMessageRemove(ctx context.Context, aVip *vip.VIP) error {
	minAnnoun, _ := aVip.GetStarlingAnnounce()
	if minAnnoun.GetTypeAnnounce() != starling_announce.StarlingAnnounce_LAYER2 || aVip.IsWildcard() {
		return nil
	}
	h.l2Announcer.DeleteBalancer(aVip.ToIpNet().String())
	return nil
}

func (h *Layer2Handler) Name() string {
	return "layer2"
}
