package ipams

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
)

// IPInterval is the allocatable ips of one subnet or ip range of the pool of a zone.
type IPInterval struct {
	Start netip.Addr
	End   netip.Addr
	Pool  string
}

// intervalsFromZone gives one interval per declared subnet or ip range, never merged: growing a pool with
// a range next to an existing one must stay an insert, not a change of the bounds of a range holding ips.
func intervalsFromZone(
	zoneInfo *zone.Zone,
) ([]IPInterval, error) {
	if zoneInfo == nil {
		return nil, errors.New("zone is nil")
	}

	var intervals []IPInterval
	for _, pool := range zoneInfo.GetPools() {
		poolIntervals, err := intervalsFromPool(pool)
		if err != nil {
			return nil, fmt.Errorf("pool %q: %w", pool.GetName(), err)
		}
		intervals = append(intervals, poolIntervals...)
	}
	return intervals, nil
}

func intervalsFromPool(pool *zone.IpPool) ([]IPInterval, error) {
	var intervals []IPInterval

	for _, subnet := range pool.GetSubnets() {
		ip, err := netip.ParseAddr(subnet.GetIp())
		if err != nil {
			return nil, err
		}

		ip = ip.Unmap()

		bits := int(subnet.GetPrefixLength())
		maxBits := ip.BitLen()

		if bits < 0 || bits > maxBits {
			return nil, fmt.Errorf(
				"invalid prefix length %d for %s",
				bits, ip,
			)
		}

		prefix := netip.PrefixFrom(ip, bits).Masked()

		start := prefix.Addr()
		end := lastAddress(prefix)

		// Reserve the network and broadcast addresses
		// for ordinary IPv4 subnets.
		if ip.Is4() && bits <= 30 {
			start = start.Next()
			end = end.Prev()
		}

		intervals = append(intervals, IPInterval{
			Start: start,
			End:   end,
			Pool:  pool.GetName(),
		})
	}

	for _, r := range pool.GetIpRanges() {
		start, err := netip.ParseAddr(r.GetIpMin())
		if err != nil {
			return nil, err
		}

		end, err := netip.ParseAddr(r.GetIpMax())
		if err != nil {
			return nil, err
		}

		start = start.Unmap()
		end = end.Unmap()

		if start.Is4() != end.Is4() ||
			start.Compare(end) > 0 {
			return nil, fmt.Errorf(
				"invalid IP range %s - %s", start, end,
			)
		}

		intervals = append(intervals, IPInterval{
			Start: start,
			End:   end,
			Pool:  pool.GetName(),
		})
	}

	return intervals, nil
}

func lastAddress(prefix netip.Prefix) netip.Addr {
	ip := prefix.Masked().Addr()
	raw := append([]byte(nil), encodeIP(ip)...)

	hostBits := ip.BitLen() - prefix.Bits()

	for i := len(raw) - 1; i >= 0 && hostBits > 0; i-- {
		n := min(hostBits, 8)
		raw[i] |= byte((1 << n) - 1)
		hostBits -= n
	}

	result, _ := netip.AddrFromSlice(raw)
	return result
}

// ValidateZone refuses a zone whose pools share a name or whose intervals overlap: an ip must name its
// pool, AcquireSpecificIP finds the range of an ip whatever its pool.
func ValidateZone(z *zone.Zone) error {
	seenNames := make(map[string]bool)
	for _, pool := range z.GetPools() {
		if seenNames[pool.GetName()] {
			return fmt.Errorf("pool %q is declared more than once", pool.GetName())
		}
		seenNames[pool.GetName()] = true
	}

	intervals, err := intervalsFromZone(z)
	if err != nil {
		return err
	}
	slices.SortFunc(intervals, func(a, b IPInterval) int {
		return a.Start.Compare(b.Start)
	})
	// sorted by start (ipv4 first), an overlap is always between neighbours of the same family
	for i := 1; i < len(intervals); i++ {
		previous, current := intervals[i-1], intervals[i]
		sameFamily := previous.Start.Is4() == current.Start.Is4()
		overlaps := sameFamily && current.Start.Compare(previous.End) <= 0
		if overlaps {
			return fmt.Errorf(
				"ips %s - %s of pool %q overlap ips %s - %s of pool %q",
				current.Start, current.End, current.Pool,
				previous.Start, previous.End, previous.Pool,
			)
		}
	}
	return nil
}
