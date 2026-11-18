package bgp

import (
	"context"
	"fmt"
	"net/netip"
	"slices"

	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
)

// AddVip will update peers of a host
func (b *Server) AddVip(ctx context.Context, aVip *vip.VIP) error {
	_, loaded := b.refVips.LoadOrStore(aVip.ToIpNet().String(), true)
	if loaded {
		return nil
	}
	b.entry.Info().Str("vip", aVip.ToIpNet().String()).Msg("Adding vip ...")
	path, err := b.getPath(aVip)
	if err == nil {
		err = b.addPath(path)
	}
	if err != nil {
		stats.vipsFail.WithLabelValues(aVip.ToIpNet().String()).Inc()
		stats.vipsSuccess.WithLabelValues(aVip.ToIpNet().String()).Set(0)
		b.refVips.Delete(aVip.ToIpNet().String())
		return err
	}
	b.entry.Info().Str("vip", aVip.ToIpNet().String()).Msg("Finished adding vip")
	stats.vipsSuccess.WithLabelValues(aVip.ToIpNet().String()).Inc()
	stats.AddAnnounced(aVip.ToIpNet().String())

	return nil
}

// DelVip will inform peers to remove a host
func (b *Server) DelVip(ctx context.Context, aVip *vip.VIP) error {
	_, loaded := b.refVips.Load(aVip.ToIpNet().String())
	if !loaded {
		return nil
	}
	b.entry.Info().Str("vip", aVip.ToIpNet().String()).Msg("Deleting vip ...")
	path, err := b.getPath(aVip)
	if err == nil {
		err = b.bgpServ.DeletePath(apiutil.DeletePathRequest{Paths: []*apiutil.Path{path}})
	}
	if err != nil {
		stats.vipsFail.WithLabelValues(aVip.ToIpNet().String()).Inc()
		return err
	}
	b.entry.Info().Str("vip", aVip.ToIpNet().String()).Msg("Finished deleting vip")
	stats.vipsSuccess.WithLabelValues(aVip.ToIpNet().String()).Inc()
	stats.RemoveAnnounced(aVip.ToIpNet().String())
	b.refVips.Delete(aVip.ToIpNet().String())
	return nil
}

func (b *Server) addPath(path *apiutil.Path) error {
	responses, err := b.bgpServ.AddPath(apiutil.AddPathRequest{Paths: []*apiutil.Path{path}})
	if err != nil {
		return err
	}
	for _, response := range responses {
		if response.Error != nil {
			return response.Error
		}
	}
	return nil
}

func (b *Server) getPath(aVip *vip.VIP) (*apiutil.Path, error) {
	addr, err := netip.ParseAddr(aVip.Ip)
	if err != nil {
		return nil, fmt.Errorf("invalid vip ip %s: %w", aVip.Ip, err)
	}
	prefixLen := int(aVip.PrefixLength)
	if prefixLen == 0 {
		prefixLen = addr.BitLen()
	}
	nlri, err := bgp.NewIPAddrPrefix(netip.PrefixFrom(addr, prefixLen))
	if err != nil {
		return nil, fmt.Errorf("invalid vip prefix %s/%d: %w", aVip.Ip, prefixLen, err)
	}

	family := bgp.RF_IPv4_UC
	// next hops are unspecified, gobgp fills them with the session address
	var nextHop bgp.PathAttributeInterface
	if addr.Is4() {
		nextHop, err = bgp.NewPathAttributeNextHop(netip.IPv4Unspecified())
	} else {
		family = bgp.RF_IPv6_UC
		nextHop, err = bgp.NewPathAttributeMpReachNLRI(family, []bgp.PathNLRI{{NLRI: nlri}}, netip.IPv6Unspecified())
	}
	if err != nil {
		return nil, err
	}

	attrs := slices.Concat([]bgp.PathAttributeInterface{bgp.NewPathAttributeOrigin(0), nextHop}, b.advAttrs(aVip))
	return &apiutil.Path{Family: family, Nlri: nlri, Attrs: attrs}, nil
}

// advAttrs merges the communities and keeps the highest local pref of the advertisements matching the vip
func (b *Server) advAttrs(aVip *vip.VIP) []bgp.PathAttributeInterface {
	comms := make([]uint32, 0)
	localPref := uint32(0)
	for _, adv := range b.findAdvs(aVip) {
		for _, comm := range adv.Communities {
			// a standard community is one 32 bits value: asn on the high 16 bits
			comms = append(comms, comm.Asn<<16|comm.Value)
		}
		localPref = max(localPref, adv.LocalPref)
	}
	attrs := make([]bgp.PathAttributeInterface, 0)
	if len(comms) > 0 {
		attrs = append(attrs, bgp.NewPathAttributeCommunities(comms))
	}
	if localPref > 0 {
		attrs = append(attrs, bgp.NewPathAttributeLocalPref(localPref))
	}
	return attrs
}

func (b *Server) findAdvs(aVip *vip.VIP) []*config.BGPAdvConfig {
	finalAdvs := make([]*config.BGPAdvConfig, 0)
	for _, adv := range b.advs {
		if len(adv.CIDRs) == 0 {
			finalAdvs = append(finalAdvs, adv)
			continue
		}

		for _, cidr := range adv.CIDRs {
			if cidr.IPNet.Contains(aVip.ToIpNet().IP) {
				finalAdvs = append(finalAdvs, adv)
				break
			}
		}
	}
	return finalAdvs
}
