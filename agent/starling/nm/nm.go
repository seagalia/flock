package nm

import (
	"context"
	"fmt"
	"net"
	"slices"

	"github.com/seagalia/flock/agent/starling/config"
)

//go:generate go tool mockgen -destination=../testhelpers/mock_nm.go -package=testhelpers -source=nm.go NetworkManager

type Route struct {
	Destination *net.IPNet
	Gateway     net.IP
	Interface   *net.Interface
	Metric      uint32
}

// NetworkManager makes vips local on the dummy interface flock0, a single ip vip as an address,
// a prefix vip as an AnyIP local route (see https://jasonmurray.org/posts/2021/anyip/):
//
//	ip -4 addr add <ip>/32 dev flock0 scope host noprefixroute
//	ip -4 route add local <cidr> dev flock0 table local
//
// The arp_ignore=1 and arp_announce=2 sysctls (see preInitOS) keep the kernel from answering ARP for them:
// only the l2 announcer answers (it skips flock0, a NOARP interface), nobody with bgp.
type NetworkManager interface {
	AddVip(ipNet *net.IPNet) error
	DelVip(ipNet *net.IPNet) error
	AddSnat(ipNet *net.IPNet, port uint16, isUdp bool, logged bool) error
	DelSnat(ipNet *net.IPNet, port uint16, isUdp bool, logged bool) error

	AddConnLimit(ipNet *net.IPNet, port uint16, isUdp bool, limit uint64, withSrcAddr bool, withDestAddr bool) error
	DelConnLimit(ipNet *net.IPNet, port uint16, isUdp bool, limit uint64, withSrcAddr bool, withDestAddr bool) error

	AddConnRateLimit(ipNet *net.IPNet, port uint16, isUdp bool, rate string, burst uint64, withSrcAddr bool, withDestAddr bool) error
	DelConnRateLimit(ipNet *net.IPNet, port uint16, isUdp bool, rate string, burst uint64, withSrcAddr bool, withDestAddr bool) error

	AddMarkLog(ipNet *net.IPNet, port uint16, isUdp bool) error
	DelMarkLog(ipNet *net.IPNet, port uint16, isUdp bool) error
	AddNfLog(ipNet *net.IPNet, port uint16, prefix string, isUdp bool) error
	DelNfLog(ipNet *net.IPNet, port uint16, prefix string, isUdp bool) error

	RouteGetAll(ip net.IP) ([]*Route, error)

	// SyncWildcards follows a change of the addresses of the machine on the rules of the wildcard vips
	SyncWildcards(ctx context.Context) error
}

var snater func(conf config.NetworkManagerConfig, ifaces []string) error

var cleaner func(conf config.NetworkManagerConfig) error

func SnatInterfaces(conf config.NetworkManagerConfig, ifaces []string) error {
	if snater != nil {
		return snater(conf, ifaces)
	}
	return nil
}

func Cleanup(conf config.NetworkManagerConfig) error {
	if cleaner != nil {
		return cleaner(conf)
	}
	return nil
}

// Vips lists the addresses of the dummy interface, none when it doesn't exist
func Vips(conf config.NetworkManagerConfig) ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	idx := slices.IndexFunc(ifaces, func(iface net.Interface) bool {
		return iface.Name == conf.DummyInterfaceName
	})
	if idx < 0 {
		return nil, nil
	}
	addrs, err := ifaces[idx].Addrs()
	if err != nil {
		return nil, fmt.Errorf("list addresses of %s: %w", conf.DummyInterfaceName, err)
	}
	var vips []net.IP
	for _, addr := range addrs {
		ipNet, isIpNet := addr.(*net.IPNet)
		// the kernel gives the interface an ipv6 link-local address of its own, it is no vip
		isVip := isIpNet && !ipNet.IP.IsLinkLocalUnicast()
		if isVip {
			vips = append(vips, ipNet.IP)
		}
	}
	return vips, nil
}
