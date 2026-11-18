package ipvsctl

import (
	"net"
	"net/netip"

	"github.com/cloudflare/ipvs/netmask"
)

// AddrFromIP converts a net.IP to the ipvs address type, ipv4 stays a 4 bytes address
func AddrFromIP(ip net.IP) netip.Addr {
	addr, _ := netip.AddrFromSlice(ip)
	return addr.Unmap()
}

// MaskFromIPMask converts a net.IPMask to the ipvs mask type
func MaskFromIPMask(mask net.IPMask) netmask.Mask {
	converted, _ := netmask.MaskFromSlice(mask)
	return converted
}
