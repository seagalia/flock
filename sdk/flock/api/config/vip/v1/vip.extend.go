package vip

import (
	"fmt"
	"net"
	"strings"

	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

func (x *VIP) Signature() (string, error) {
	return helpers.MessageSignature(x)
}

func (x *VIP) GetIdentifier() string {
	return x.GetUUID()
}

func (x *VIP) SetIdentifier(identifier string) {
	x.UUID = identifier
}

func (x *VIP) IsIPV6() bool {
	if x == nil {
		return false
	}
	return strings.Contains(x.GetIp(), ":")
}

// IsWildcard tells a VIP whose ip is unspecified (0.0.0.0 or ::): it stands for every address of the machine of its
// family, whatever its prefix length.
func (x *VIP) IsWildcard() bool {
	return net.ParseIP(x.GetIp()).IsUnspecified()
}

func (x *VIP) IsIPV4() bool {
	if x == nil {
		return false
	}
	return !x.IsIPV6()
}

// ProvDefault gives a VIP without a prefix length the one of a single address, as the agent announces it. A VIP
// without an ip keeps 0, a registered VIP gets the prefix of the ip the ipam acquires for it.
func (x *VIP) ProvDefault() {
	if x == nil || x.GetPrefixLength() != 0 || x.GetIp() == "" {
		return
	}
	x.PrefixLength = 32
	if x.IsIPV6() {
		x.PrefixLength = 128
	}
}

func (x *VIP) ToIpNet() *net.IPNet {
	if x == nil {
		return nil
	}
	ip := net.ParseIP(x.GetIp())

	if x.GetPrefixLength() > 0 && (x.GetPrefixLength() <= 128 && x.IsIPV6() || x.GetPrefixLength() <= 32 && x.IsIPV4()) {
		_, cidr, _ := net.ParseCIDR(fmt.Sprintf("%s/%d", ip.String(), x.GetPrefixLength())) // nolint
		return cidr
	}
	if x.IsIPV4() {
		_, cidr, _ := net.ParseCIDR(fmt.Sprintf("%s/32", ip.String())) // nolint
		return cidr
	}
	_, cidr, _ := net.ParseCIDR(fmt.Sprintf("%s/128", ip.String())) // nolint

	return cidr
}

type VIPs []*VIP

func (x VIPs) Contains(aVip *VIP) bool {
	for _, vip := range x {
		if vip.GetIdentifier() == aVip.GetIdentifier() {
			return true
		}
	}
	return false
}

func (x VIPs) ContainsByIpNet(aVip *VIP) bool {
	for _, vip := range x {
		if vip.ToIpNet().String() == aVip.ToIpNet().String() {
			return true
		}
	}
	return false
}

func (x *VIP) GetStarlingAnnounce() (*starling_announce.StarlingAnnounce, error) {
	options := &starling_announce.StarlingAnnounce{}
	if x.GetTypedAnnounce() == nil {
		return options, nil
	}
	err := x.GetTypedAnnounce().UnmarshalTo(options)
	if err != nil {
		return options, err
	}
	return options, nil
}

func (x *VIP) ForeignResources() map[string][]string {
	return map[string][]string{
		helpers.TypeUrl[*vs.VirtualServer](): x.GetVirtualServersUuid(),
	}
}
func (x *VIP) AddMetadata(key, value string) {
	if x.Metadata == nil {
		x.Metadata = make(map[string]string)
	}
	x.Metadata[key] = value
}

func (x *VIP) DeleteMetadata(key string) map[string]string {
	if x.Metadata == nil {
		return nil
	}
	delete(x.Metadata, key)
	return x.Metadata
}

func (x *VIP) SetMetadata(m map[string]string) {
	x.Metadata = m
}
