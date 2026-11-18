package devenv

import (
	"fmt"
	"net"
	"slices"

	starlingconfig "github.com/seagalia/flock/agent/starling/config"
)

// listSnatIfaces lists every interface IPVS may send a connection out of, one per network of a container: the agent
// source-nats on each so the backends behind any of them answer through this machine.
func listSnatIfaces() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}
	var names []string
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("addresses of %s: %w", iface.Name, err)
		}
		if isSnatIface(iface, addrs) {
			names = append(names, iface.Name)
		}
	}
	return names, nil
}

// isSnatIface leaves out the loopback, which the agent config always lists, the interface of the vips, and an
// interface with only link-local addresses, which can't be the source of a connection to another network
func isSnatIface(iface net.Interface, addrs []net.Addr) bool {
	isUp := iface.Flags&net.FlagUp != 0
	isLoopback := iface.Flags&net.FlagLoopback != 0
	isVipIface := iface.Name == starlingconfig.DefaultDummyInterfaceName
	if !isUp || isLoopback || isVipIface {
		return false
	}
	return slices.ContainsFunc(addrs, func(addr net.Addr) bool {
		ipNet, ok := addr.(*net.IPNet)
		return ok && ipNet.IP.IsGlobalUnicast()
	})
}

// localIPs feeds the SANs of the controller certificate, hence the skipped link-local addresses: a client can't dial
// them without a zone.
func localIPs() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}
	var ips []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("addresses of %s: %w", iface.Name, err)
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if ok && keepSanIP(ipNet.IP) {
				ips = append(ips, ipNet.IP)
			}
		}
	}
	return ips, nil
}

func keepSanIP(ip net.IP) bool {
	return !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}
