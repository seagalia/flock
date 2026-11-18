package core

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	_ "google.golang.org/protobuf/types/known/anypb"
	_ "google.golang.org/protobuf/types/known/structpb"
)

func (x *IPAddr) Format() string {
	if x == nil {
		return ""
	}
	return net.JoinHostPort(x.GetIp(), strconv.Itoa(int(x.GetPort())))
}

func (x *IPAddr) IsIPV6() bool {
	if x == nil {
		return false
	}
	return strings.Contains(x.GetIp(), ":")
}

func (x *IPAddr) IsIPV4() bool {
	if x == nil {
		return false
	}
	return !x.IsIPV6()
}

func (x *IPAddr) ToIpNet() *net.IPNet {
	if x == nil {
		return nil
	}
	// error could not happen with this prefix
	var cidr *net.IPNet
	if x.IsIPV4() {
		_, cidr, _ = net.ParseCIDR(fmt.Sprintf("%s/32", x.GetIp())) // nolint
	} else {
		_, cidr, _ = net.ParseCIDR(fmt.Sprintf("%s/128", x.GetIp())) // nolint
	}
	return cidr
}

func (x *IPAddr) Clone() *IPAddr {
	if x == nil {
		return nil
	}
	return &IPAddr{
		Ip:                  x.GetIp(),
		Port:                x.GetPort(),
		LoadBalancingWeight: x.GetLoadBalancingWeight(),
	}
}

func (x *IPAddrGroup) ToIps() []net.IP {
	if x == nil {
		return nil
	}
	ips := make([]net.IP, 0)
	for _, ip := range x.GetIpAddresses() {
		ipRaw := net.ParseIP(ip.GetIp())
		if ipRaw == nil {
			continue
		}
		ips = append(ips, ipRaw)
	}
	return ips

}
