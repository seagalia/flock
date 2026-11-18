package agentserv

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/r3labs/sse/v2"
	"github.com/rs/zerolog"
	"github.com/samber/lo"
	"github.com/seagalia/flock/agent/starling/agentlist"
	"github.com/seagalia/flock/agent/starling/nm"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/logging"
	"net"
	"net/http"
	"sync"
)

const (
	AnnouncedVipsEvent = "announced_vips"
)

type IfaceInfo struct {
	Addrs   []string `json:"addrs"`
	MacAddr string   `json:"mac_addr"`
}

type CompanionServer struct {
	entry     zerolog.Logger
	vipPlacer *agentlist.VipPlacer
	sseServer *sse.Server
	// last announced vips event, replayed to new subscribers
	lastEvent      []byte
	mutexLastEvent *sync.RWMutex
	advIfaces      []string
	nm             nm.NetworkManager
}

func NewCompanionServer(vipPlacer *agentlist.VipPlacer, netManager nm.NetworkManager, advIfaces []string) *CompanionServer {
	sseServer := sse.New()
	// sse event log is never purged and can't be safely trimmed outside the library, last event is replayed here
	sseServer.AutoReplay = false
	cs := &CompanionServer{
		entry:          logging.Component("companion_server"),
		vipPlacer:      vipPlacer,
		sseServer:      sseServer,
		mutexLastEvent: &sync.RWMutex{},
		advIfaces:      advIfaces,
		nm:             netManager,
	}
	if vipPlacer != nil {
		vipPlacer.AddPlacerListener(cs)
		sseServer.CreateStream(AnnouncedVipsEvent)
	}
	return cs
}

func (c *CompanionServer) OnPlacerAddedVip(ctx context.Context, aVip *vip.VIP) error {
	return nil
}

func (c *CompanionServer) OnPlacerRemoveVip(ctx context.Context, aVip *vip.VIP) error {
	return nil
}

func (c *CompanionServer) OnAfterCalculate(ctx context.Context, vips []*vip.VIP) error {
	inlineVips := make([]string, len(vips))
	for i, v := range vips {
		inlineVips[i] = v.ToIpNet().String()
	}
	b, err := json.Marshal(inlineVips)
	if err != nil {
		return fmt.Errorf("failed to marshal vips: %w", err)
	}
	c.mutexLastEvent.Lock()
	c.lastEvent = b
	c.mutexLastEvent.Unlock()
	c.sseServer.Publish(AnnouncedVipsEvent, &sse.Event{
		Data: b,
	})
	return nil
}

func (c *CompanionServer) ServeEvents(w http.ResponseWriter, req *http.Request) {
	c.mutexLastEvent.RLock()
	lastEvent := c.lastEvent
	c.mutexLastEvent.RUnlock()
	flusher, canFlush := w.(http.Flusher)
	isVipsStream := req.URL.Query().Get("stream") == AnnouncedVipsEvent
	canReplay := lastEvent != nil && canFlush
	if isVipsStream && canReplay {
		// same headers as the sse server sets
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		fmt.Fprintf(w, "data: %s\n\n", lastEvent)
		flusher.Flush()
	}
	c.sseServer.ServeHTTP(w, req)
}

func (c *CompanionServer) ServeInterfacesInfo(w http.ResponseWriter, req *http.Request) {
	if len(c.advIfaces) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}
	ifaces := make([]*net.Interface, 0)
	for _, ifaceName := range c.advIfaces {
		iface, err := net.InterfaceByName(ifaceName)
		if err != nil {
			c.entry.Error().Msgf("failed to get interface %s: %v", ifaceName, err)
			continue
		}
		ifaces = append(ifaces, iface)
	}

	ipDest := req.URL.Query().Get("for_dest")
	if ipDest == "" {
		c.serveInterfacesInfoFromList(ifaces, w)
		return
	}

	rtes, err := c.nm.RouteGetAll(net.ParseIP(ipDest))
	if err != nil {
		c.entry.Error().Msgf("failed to get interfaces for destination %s: %v", ipDest, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if len(rtes) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}
	finalIfaces := make(map[string]*net.Interface, 0)
	for _, rte := range rtes {
		if !lo.ContainsBy[*net.Interface](ifaces, func(i *net.Interface) bool {
			return i.HardwareAddr.String() == rte.Interface.HardwareAddr.String()
		}) {
			continue
		}
		finalIfaces[rte.Interface.HardwareAddr.String()] = rte.Interface
	}
	ifaces = make([]*net.Interface, 0, len(finalIfaces))
	for _, iface := range finalIfaces {
		ifaces = append(ifaces, iface)
	}
	c.serveInterfacesInfoFromList(ifaces, w)
}

func (c *CompanionServer) serveInterfacesInfoFromList(ifaces []*net.Interface, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	ifacesInfo := make([]IfaceInfo, 0)
	for _, ifaceInfo := range ifaces {
		addrs, err := ifaceInfo.Addrs()
		if err != nil {
			c.entry.Error().Msgf("failed to get addresses for interface %s, skipping: %v", ifaceInfo.Name, err)
			continue
		}
		addrsStr := make([]string, len(addrs))
		for i, addr := range addrs {
			addrsStr[i] = addr.String()
		}
		ifacesInfo = append(ifacesInfo, IfaceInfo{
			Addrs:   addrsStr,
			MacAddr: ifaceInfo.HardwareAddr.String(),
		})
	}

	b, err := json.Marshal(ifacesInfo)
	if err != nil {
		c.entry.Error().Msgf("failed to marshal interfaces info: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

func (c *CompanionServer) Stop() {
	if c.vipPlacer != nil {
		c.vipPlacer.RemovePlacerListener(c)
	}
	c.sseServer.Close()
}
