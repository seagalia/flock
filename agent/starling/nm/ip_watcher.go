package nm

import (
	"bytes"
	"net"
	"slices"
	"syscall"
	"time"
)

const (
	retryMinWait = time.Second
	retryMaxWait = 30 * time.Second
)

type machineAddr struct {
	ifaceIndex uint32
	ip         net.IP
}

// expandWildcard gives the addresses a wildcard VIP of the family stands for: the global addresses of every interface
// but the dummy one, loopback and link-local excluded, duplicates collapsed, sorted.
func expandWildcard(addrs []machineAddr, family int, dummyIndex uint32) []net.IP {
	seen := make(map[string]bool)
	expanded := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		isDummy := a.ifaceIndex == dummyIndex
		isGlobal := !a.ip.IsLoopback() && !a.ip.IsLinkLocalUnicast() && !a.ip.IsMulticast() && !a.ip.IsUnspecified()
		isFamily := (family == syscall.AF_INET) == (a.ip.To4() != nil)
		if isDummy || !isGlobal || !isFamily {
			continue
		}
		ip := a.ip
		if family == syscall.AF_INET {
			ip = a.ip.To4()
		}
		if seen[string(ip)] {
			continue
		}
		seen[string(ip)] = true
		expanded = append(expanded, ip)
	}
	slices.SortFunc(expanded, func(a, b net.IP) int { return bytes.Compare(a, b) })
	return expanded
}

func sameIPs(a, b []net.IP) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

func nextRetryWait(wait time.Duration) time.Duration {
	return min(wait*2, retryMaxWait)
}
