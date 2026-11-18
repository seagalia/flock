package stores

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/avast/retry-go/v5"
	"github.com/samber/lo"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/storers"
)

type IpPortTranslator struct {
	cacheIpPort *sync.Map
	store       storers.StoreManager
}

// NewIpPortTranslator reads the virtual servers of the vips it is given in store.
func NewIpPortTranslator(store storers.StoreManager) *IpPortTranslator {
	return &IpPortTranslator{
		cacheIpPort: &sync.Map{},
		store:       store,
	}
}

func (t *IpPortTranslator) OnMessageAdded(ctx context.Context, aVip *vip.VIP) error {
	cachedVs := t.findCachedVsFromVip(aVip)
	storedVs, err := t.findStoredVsFromVip(ctx, aVip)
	if err != nil {
		return err
	}
	for _, virtServ := range cachedVs {
		if lo.ContainsBy[*vs.VirtualServer](storedVs, func(elem *vs.VirtualServer) bool {
			return elem.GetUUID() == virtServ.GetUUID()
		}) {
			continue
		}
		t.cacheIpPort.Delete(t.formatVipVs(aVip, virtServ))
	}
	for _, virtServ := range storedVs {
		t.cacheIpPort.Store(t.formatVipVs(aVip, virtServ), virtServ)
	}

	return nil
}

func (t *IpPortTranslator) FindVs(ip string, port uint32, protocol string) (*vs.VirtualServer, bool) {
	vsRaw, ok := t.cacheIpPort.Load(ipPortKey(ip, port, protocol))
	if !ok {
		return nil, false
	}
	return vsRaw.(*vs.VirtualServer), true
}

func (t *IpPortTranslator) formatVipVs(aVip *vip.VIP, virtServ *vs.VirtualServer) string {

	protocol := "tcp"
	if virtServ.GetUdpRouteConfig() != nil {
		protocol = "udp"
	}
	return ipPortKey(aVip.GetIp(), virtServ.GetPort(), protocol)
}

func ipPortKey(ip string, port uint32, protocol string) string {
	return net.JoinHostPort(ip, strconv.Itoa(int(port))) + "_" + protocol
}

func ipKeyPrefix(ip string) string {
	return net.JoinHostPort(ip, "")
}

func (t *IpPortTranslator) OnMessageRemove(ctx context.Context, aVip *vip.VIP) error {
	ipSearch := ipKeyPrefix(aVip.GetIp())
	t.cacheIpPort.Range(func(key, value any) bool {
		if strings.HasPrefix(key.(string), ipSearch) {
			t.cacheIpPort.Delete(key)
		}
		return true
	})
	return nil
}

func (t *IpPortTranslator) findCachedVsFromVip(aVip *vip.VIP) []*vs.VirtualServer {
	ipSearch := ipKeyPrefix(aVip.GetIp())
	var result []*vs.VirtualServer
	t.cacheIpPort.Range(func(key, value any) bool {
		if strings.HasPrefix(key.(string), ipSearch) {
			result = append(result, value.(*vs.VirtualServer))
		}
		return true
	})
	return result
}

func (t *IpPortTranslator) Name() string {
	return "ip_port_translator"
}

func (t *IpPortTranslator) findStoredVsFromVip(ctx context.Context, aVip *vip.VIP) ([]*vs.VirtualServer, error) {
	var result []*vs.VirtualServer

	for _, vsUuid := range aVip.GetVirtualServersUuid() {
		var err error
		var virtServ *vs.VirtualServer
		retryErr := retry.New(retry.Attempts(3), retry.Delay(1*time.Second)).Do(func() error {
			virtServ, err = t.store.VirtualServers().Read(ctx, vsUuid)
			return err
		})
		if retryErr != nil {
			return nil, fmt.Errorf("unable to read virtual server %s: %w", vsUuid, retryErr)
		}
		result = append(result, virtServ)
	}
	return result, nil
}
