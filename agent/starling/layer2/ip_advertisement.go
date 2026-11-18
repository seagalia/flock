// licensed to metallb authors under the apache license, version 2.0
// file: https://github.com/metallb/metallb/tree/main/internal/layer2/ip_advertisement.go

package layer2

import (
	"slices"

	"github.com/samber/lo"
	"net/netip"
)

// IPAdvertisement is the advertisement Info about LB IP.
type IPAdvertisement struct {
	ip            netip.Addr
	interfaces    []string
	allInterfaces bool
}

func NewIPAdvertisement(ip netip.Addr, allInterfaces bool, interfaces []string) IPAdvertisement {
	return IPAdvertisement{
		ip:            ip,
		interfaces:    interfaces,
		allInterfaces: allInterfaces,
	}
}

func (s *IPAdvertisement) Equal(t *IPAdvertisement) bool {
	if s == nil && t == nil {
		return true
	}
	if s == nil || t == nil {
		return false
	}
	if s.ip.Compare(t.ip) != 0 {
		return false
	}
	if s.allInterfaces != t.allInterfaces {
		return false
	}
	if s.allInterfaces {
		return true
	}
	hasAll := true
	for _, intf := range s.interfaces {
		if !lo.Contains[string](t.interfaces, intf) {
			hasAll = false
			break
		}
	}
	return hasAll
}

func (s *IPAdvertisement) MatchInterfaces(intfs ...string) bool {
	return slices.ContainsFunc(intfs, s.matchInterface)
}

func (s *IPAdvertisement) matchInterface(intf string) bool {
	if s == nil {
		return false
	}
	if s.allInterfaces {
		return true
	}
	return lo.Contains[string](s.interfaces, intf)
}
