package ipams

import (
	"context"
	"errors"
	"net/netip"

	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
)

var (
	ErrPoolExhausted  = errors.New("IPAM pool exhausted")
	ErrIPOutOfRange   = errors.New("IP is outside pool ranges")
	ErrIPAllocated    = errors.New("IP already allocated")
	ErrIPNotAllocated = errors.New("IP not allocated")
	// ErrNoAllocatableIP is given by a zone without subnets nor ip ranges asked for an ip
	ErrNoAllocatableIP = errors.New("zone has no subnets nor ip ranges to allocate an IP from")
	ErrIPsOutOfRanges  = errors.New("a range holding an allocated IP can not change its bounds nor be removed")
	ErrPoolNotFound    = errors.New("pool not found in zone")
)

type IPRange struct {
	ID       string
	PoolID   string
	PoolName string
	Family   int
	Start    netip.Addr
	End      netip.Addr
	Cursor   netip.Addr // Invalid when exhausted.
	Priority int
}

func encodeIP(ip netip.Addr) []byte {
	ip = ip.Unmap()
	if ip.Is4() {
		a := ip.As4()
		return a[:]
	}
	a := ip.As16()
	return a[:]
}

func decodeIP(data []byte) (netip.Addr, error) {
	ip, ok := netip.AddrFromSlice(data)
	if !ok || (len(data) != 4 && len(data) != 16) {
		return netip.Addr{}, errors.New("invalid binary IP")
	}
	return ip.Unmap(), nil
}

func ipFamily(ip netip.Addr) int {
	if ip.Unmap().Is4() {
		return 4
	}
	return 6
}

func containsIP(r IPRange, ip netip.Addr) bool {
	ip = ip.Unmap()
	return ipFamily(ip) == r.Family &&
		ip.Compare(r.Start) >= 0 &&
		ip.Compare(r.End) <= 0
}

func nextIP(ip, end netip.Addr) netip.Addr {
	if ip.Compare(end) >= 0 {
		return netip.Addr{}
	}
	next := ip.Next()
	if !next.IsValid() || next.Compare(end) > 0 {
		return netip.Addr{}
	}
	return next
}

// Ipam allocates the ips of the vips in the pools of a zone, the owner of an allocation is the vip uuid.
// A zone without pools is unmanaged: AcquireIP fails with ErrNoAllocatableIP, the other calls but
// SyncZone do nothing, so a vip of such a zone keeps the ip it was given.
type Ipam interface {
	// AcquireIP gives the next free ip of the pool named pool in the zone, released ips first.
	// It fails with ErrPoolNotFound when the zone has no such pool and ErrPoolExhausted when every
	// ip of the pool is taken. Each call gives a new ip, even to an owner that already has one.
	AcquireIP(
		ctx context.Context,
		aZone *zone.Zone,
		pool string,
		owner string,
	) (netip.Addr, error)
	// AcquireSpecificIP records ip as taken by owner, in whatever pool of the zone holds it: it is how a
	// vip with a fixed ip, or attached to an instance of another zone, gets its ip. Taking again an ip the
	// owner already has succeeds, an ip of another owner fails with ErrIPAllocated and an ip out of
	// every range with ErrIPOutOfRange.
	AcquireSpecificIP(
		ctx context.Context,
		aZone *zone.Zone,
		ip netip.Addr,
		owner string,
	) error
	// ReleaseIP gives ip back to its range so a next AcquireIP can take it again. It does not check
	// the pools of the zone: an ip must stay releasable after its pool changed.
	ReleaseIP(
		ctx context.Context,
		aZone *zone.Zone,
		ip netip.Addr,
	) error
	// ReleaseOwner releases every ip of the owner, in all zones: when a vip is deleted or its creation failed.
	ReleaseOwner(
		ctx context.Context,
		owner string,
	) error
	// HasAllocatedIPs tells whether an ip is still taken in the zone, whatever its pool.
	HasAllocatedIPs(
		ctx context.Context,
		aZone *zone.Zone,
	) (bool, error)
	// SyncZone makes the ranges of the zone match its pools. A range is matched on its bounds: it refuses
	// (ErrIPsOutOfRanges) to change the bounds of a range holding an allocated ip or to remove it, even if the
	// ip would still fit, and changes nothing then. A zone without pools frees all its ranges, which is how
	// a deleted zone is cleaned up.
	SyncZone(
		ctx context.Context,
		aZone *zone.Zone,
	) error
}
