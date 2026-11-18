package agentserv

import (
	"context"
	"maps"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/seagalia/flock/agent/f5/bigip"
)

// CachedDevice keeps the status reads off the BIG-IP: iControl REST copes badly with many requests per second, and
// every refresh of the dashboard or the cli would read each device and each pool again. Concurrent callers share one
// read, a failed read is not kept.
type CachedDevice struct {
	dev   Device
	ttl   time.Duration
	reads singleflight.Group

	mu      sync.Mutex
	devices cachedDevices
	pools   map[string]cachedPool
}

type cachedDevices struct {
	devices []bigip.DeviceHealth
	readAt  time.Time
}

type cachedPool struct {
	states map[string]bool
	readAt time.Time
}

func NewCachedDevice(dev Device, ttl time.Duration) *CachedDevice {
	return &CachedDevice{dev: dev, ttl: ttl, pools: map[string]cachedPool{}}
}

func (c *CachedDevice) Devices(ctx context.Context) []bigip.DeviceHealth {
	// the read is shared, a caller going away must not fail it for the others
	read := c.reads.DoChan("devices", func() (any, error) {
		c.mu.Lock()
		cached := c.devices
		c.mu.Unlock()
		if time.Since(cached.readAt) < c.ttl {
			return cached.devices, nil
		}
		devices := c.dev.Devices(context.WithoutCancel(ctx))
		c.mu.Lock()
		c.devices = cachedDevices{devices: devices, readAt: time.Now()}
		c.mu.Unlock()
		return devices, nil
	})
	select {
	case <-ctx.Done():
		return nil
	case result := <-read:
		return result.Val.([]bigip.DeviceHealth)
	}
}

func (c *CachedDevice) PoolMemberStates(ctx context.Context, tenant, app, pool string) (map[string]bool, error) {
	key := tenant + "/" + app + "/" + pool
	read := c.reads.DoChan("pool "+key, func() (any, error) {
		c.mu.Lock()
		cached, isCached := c.pools[key]
		c.mu.Unlock()
		if isCached && time.Since(cached.readAt) < c.ttl {
			return cached.states, nil
		}
		states, err := c.dev.PoolMemberStates(context.WithoutCancel(ctx), tenant, app, pool)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		// the pools of deleted VIPs would stay forever otherwise
		maps.DeleteFunc(c.pools, func(_ string, p cachedPool) bool { return time.Since(p.readAt) >= c.ttl })
		c.pools[key] = cachedPool{states: states, readAt: time.Now()}
		c.mu.Unlock()
		return states, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-read:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.(map[string]bool), nil
	}
}
