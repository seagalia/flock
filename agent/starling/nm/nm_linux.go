//go:build linux

package nm

import (
	"context"
	"fmt"
	"net"
	"time"

	"errors"

	"github.com/hashicorp/go-multierror"
	"github.com/jsimonetti/rtnetlink/v2"
	"github.com/jsimonetti/rtnetlink/v2/rtnl"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/sdk/logging"
	"golang.org/x/sys/unix"
	"sigs.k8s.io/knftables"
)

// the NetworkManager interface carries no ctx, each nft transaction bounds its own run of the nft binary
const nftTransactionTimeout = 30 * time.Second

func init() {
	snater = nftSnater
	cleaner = cleanup
}

func addrFamily(ip net.IP) (int, error) {
	if ip.To4() != nil {
		return unix.AF_INET, nil
	}
	if len(ip) == net.IPv6len {
		return unix.AF_INET6, nil
	}
	return 0, &net.AddrError{Err: "invalid IP address", Addr: ip.String()}
}

type nmLinux struct {
	entry          zerolog.Logger
	dummyInterface *net.Interface
	conn           *rtnl.Conn
	rules          *nftRules
	conf           config.NetworkManagerConfig
}

func newNft() (knftables.Interface, error) {
	nft, err := knftables.New(knftables.InetFamily, nftTable)
	if err != nil {
		return nil, fmt.Errorf("nftables is not usable, the agent needs the nft binary (>= 1.0.1) and kernel >= 5.10: %w", err)
	}
	return nft, nil
}

// NewNetworkManager sets the rules asked for a wildcard vip on each address addresses gives, nil sets them nowhere
func NewNetworkManager(conf config.NetworkManagerConfig, addresses MachineIPs) (NetworkManager, error) {
	nft, err := newNft()
	if err != nil {
		return nil, err
	}
	rules := newNftRules(nft, conf, addresses)
	ctx, cancel := context.WithTimeout(context.Background(), nftTransactionTimeout)
	defer cancel()
	err = rules.setup(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to set up the nftables table %s: %w", nftTable, err)
	}

	conn, err := rtnl.Dial(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to rtnetlink: %w", err)
	}

	dummyIface, err := ensureDummyInterface(conn.Conn, conf.DummyInterfaceName)
	if err != nil {
		return nil, fmt.Errorf("failed to create dummy interface: %w", err)
	}

	return &nmLinux{
		entry:          logging.Component("network_manager"),
		dummyInterface: dummyIface,
		conn:           conn,
		rules:          rules,
		conf:           conf,
	}, nil
}

func ensureDummyInterface(c *rtnetlink.Conn, name string) (*net.Interface, error) {
	err := c.Link.New(&rtnetlink.LinkMessage{
		Attributes: &rtnetlink.LinkAttributes{
			Name: name,
			Info: &rtnetlink.LinkInfo{
				Kind: "dummy",
			},
		},
	})
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return nil, fmt.Errorf("create dummy %s: %w", name, err)
	}

	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("find interface %s: %w", name, err)
	}
	index := uint32(iface.Index)

	link, err := c.Link.Get(index)
	if err != nil {
		return nil, fmt.Errorf("inspect interface %s: %w", name, err)
	}
	hasKind := link.Attributes != nil && link.Attributes.Info != nil
	isDummy := hasKind && link.Attributes.Info.Kind == "dummy"
	if !isDummy {
		return nil, fmt.Errorf("interface %s exists but is not dummy", name)
	}

	err = c.Link.Set(&rtnetlink.LinkMessage{
		Index:  index,
		Flags:  unix.IFF_UP,
		Change: unix.IFF_UP,
	})
	if err != nil {
		return nil, fmt.Errorf("bring interface %s up: %w", name, err)
	}

	return iface, nil
}

func (n *nmLinux) Close() error {
	return n.conn.Close()
}
func (n *nmLinux) RouteGetAll(ip net.IP) ([]*Route, error) {
	rtes, err := n.conn.RouteGetAll(ip)
	if err != nil {
		return nil, fmt.Errorf("failed to get routes: %w", err)
	}
	var result []*Route
	for _, rte := range rtes {
		result = append(result, &Route{
			Destination: rte.Destination,
			Gateway:     rte.Gateway,
			Interface:   rte.Interface,
			Metric:      rte.Metric,
		})
	}
	return result, nil
}

func isWildcard(ipNet *net.IPNet) bool {
	return ipNet.IP.IsUnspecified()
}

func (n *nmLinux) SyncWildcards(ctx context.Context) error {
	return n.rules.syncWildcards(ctx)
}

// AddVip leaves a wildcard vip off the dummy interface, its addresses are already on the interfaces of the machine
func (n *nmLinux) AddVip(ipNet *net.IPNet) error {
	if isWildcard(ipNet) {
		return nil
	}
	n.entry.Debug().Stringer("ip_net", ipNet).Msg("Adding vip on interface " + n.conf.DummyInterfaceName + " ...")
	err := n.setVip(true, ipNet)
	if err != nil {
		stats.vipIfaceFail.WithLabelValues(ipNet.String()).Inc()
		stats.vipIfaceSuccess.WithLabelValues(ipNet.String()).Set(0)
		return fmt.Errorf("failed to add vip on interface %s: %w", n.conf.DummyInterfaceName, err)
	}
	n.entry.Debug().Stringer("ip_net", ipNet).Msg("Finished adding vip on interface " + n.conf.DummyInterfaceName)
	stats.vipIfaceSuccess.WithLabelValues(ipNet.String()).Inc()
	stats.vipIfaceFail.WithLabelValues(ipNet.String()).Set(0)
	return nil
}

func (n *nmLinux) DelVip(ipNet *net.IPNet) error {
	if isWildcard(ipNet) {
		return nil
	}
	n.entry.Debug().Stringer("ip_net", ipNet).Msg("Deleting vip on interface " + n.conf.DummyInterfaceName + " ...")
	err := n.setVip(false, ipNet)
	if err != nil {
		stats.vipIfaceFail.WithLabelValues(ipNet.String()).Inc()
		return fmt.Errorf("failed to delete vip on interface %s: %w", n.conf.DummyInterfaceName, err)
	}
	n.entry.Debug().Stringer("ip_net", ipNet).Msg("Finished deleting vip on interface " + n.conf.DummyInterfaceName)
	stats.vipIfaceSuccess.WithLabelValues(ipNet.String()).Dec()
	stats.vipIfaceFail.WithLabelValues(ipNet.String()).Set(0)
	return nil
}

// setVip makes every ip of the vip local, IPVS (LOCAL_IN hook) and envoy then receive its traffic.
// A single ip vip is an address of flock0. A prefix vip is an AnyIP local route on flock0 instead:
// an address would only make its own ip local and add a prefix route and a broadcast route.
func (n *nmLinux) setVip(add bool, ipNet *net.IPNet) error {
	af, err := addrFamily(ipNet.IP)
	if err != nil {
		return err
	}
	ones, bits := ipNet.Mask.Size()
	if bits == 0 {
		return fmt.Errorf("invalid mask: %v", ipNet.Mask)
	}

	isSingleIp := ones == bits
	if !isSingleIp {
		rm := n.genLocalRouteMessage(af, ipNet)
		if !add {
			return n.conn.Conn.Route.Delete(rm)
		}
		return n.conn.Conn.Route.Replace(rm)
	}

	am := n.genAddressMessage(af, ipNet)
	if !add {
		err = n.conn.Conn.Address.Delete(am)
		// a vip skipped by the add was never on flock0
		if errors.Is(err, unix.EADDRNOTAVAIL) {
			return nil
		}
		return err
	}

	holder, held, err := n.ipHeld(ipNet.IP, am.Index)
	if err != nil {
		return fmt.Errorf("check if the vip is already an address of another interface of this machine: %w", err)
	}
	if held {
		n.entry.Warn().Stringer("ip", ipNet.IP).Str("interface", interfaceName(holder)).
			Msg("The vip is already an address of another interface of this machine, it is not added to " +
				n.conf.DummyInterfaceName)
		return nil
	}
	err = n.conn.Conn.Address.New(am)
	if errors.Is(err, unix.EEXIST) {
		return nil
	}
	return err
}

// ipHeld tells whether an interface other than the dummy one already has ip as an address, adding it to the
// dummy interface too would conflict with that interface (a container's own ip, for instance).
func (n *nmLinux) ipHeld(ip net.IP, dummyIndex uint32) (uint32, bool, error) {
	af, err := addrFamily(ip)
	if err != nil {
		return 0, false, err
	}
	// listed at each add, the addresses of the machine change while the agent runs
	addrs, err := n.conn.Conn.Address.List()
	if err != nil {
		return 0, false, fmt.Errorf("list the addresses of the machine: %w", err)
	}
	holder, held := findHolder(addrs, ip, af, dummyIndex)
	return holder, held, nil
}

// findHolder is the netlink-free part of ipHeld, so specs run it with no privileges
func findHolder(addrs []rtnetlink.AddressMessage, ip net.IP, af int, dummyIndex uint32) (uint32, bool) {
	sameFamily := make([]rtnetlink.AddressMessage, 0, len(addrs))
	for _, a := range addrs {
		if int(a.Family) == af {
			sameFamily = append(sameFamily, a)
		}
	}

	for _, a := range sameFamily {
		if a.Index == dummyIndex || a.Attributes == nil {
			continue
		}
		if a.Attributes.Address.Equal(ip) || a.Attributes.Local.Equal(ip) {
			return a.Index, true
		}
	}
	return 0, false
}

func interfaceName(index uint32) string {
	iface, err := net.InterfaceByIndex(int(index))
	if err != nil {
		return fmt.Sprintf("index %d", index)
	}
	return iface.Name
}

func (n *nmLinux) genAddressMessage(af int, ipNet *net.IPNet) *rtnetlink.AddressMessage {
	ones, _ := ipNet.Mask.Size()
	return &rtnetlink.AddressMessage{
		Family:       uint8(af),
		PrefixLength: uint8(ones),
		Scope:        unix.RT_SCOPE_HOST,
		Index:        uint32(n.dummyInterface.Index),
		Attributes: &rtnetlink.AddressAttributes{
			Address: ipNet.IP,
			Local:   ipNet.IP,
			// ipv6 adds a /128 route in the main table otherwise, the local table already routes the vip
			Flags: unix.IFA_F_NOPREFIXROUTE,
		},
	}
}

// genLocalRouteMessage gives the AnyIP route `ip route add local <cidr> dev flock0 table local`
func (n *nmLinux) genLocalRouteMessage(af int, ipNet *net.IPNet) *rtnetlink.RouteMessage {
	opts := rtnl.DefaultRouteOptions(n.dummyInterface, *ipNet, nil)
	ones, _ := ipNet.Mask.Size()
	return &rtnetlink.RouteMessage{
		Family:     uint8(af),
		Table:      unix.RT_TABLE_LOCAL,
		Protocol:   unix.RTPROT_BOOT,
		Type:       unix.RTN_LOCAL,
		Scope:      unix.RT_SCOPE_HOST,
		DstLength:  uint8(ones),
		Attributes: opts.Attrs,
	}
}
func deleteDummyInterface(name string) error {
	c, err := rtnetlink.Dial(nil)
	if err != nil {
		return err
	}
	defer c.Close()
	dummies, err := c.Link.ListByKind("dummy")
	if err != nil {
		return err
	}
	for _, link := range dummies {
		if link.Attributes != nil && link.Attributes.Name == name {
			return c.Link.Delete(link.Index)
		}
	}
	return nil
}

// updateVs changes the nftables rules of one virtual server, what names the change in the logs and errors
func (n *nmLinux) updateVs(what string, ipNet *net.IPNet, port uint16, isUdp bool, change func(v *vsRules)) error {
	entry := n.entry.With().Stringer("ip_net", ipNet).Uint16("port", port).Bool("is_udp", isUdp).Logger()
	entry.Debug().Msgf("%s ...", what)
	ctx, cancel := context.WithTimeout(context.Background(), nftTransactionTimeout)
	defer cancel()
	err := n.rules.updateVs(ctx, newVsKey(ipNet, port, isUdp), change)
	if err != nil {
		return fmt.Errorf("failed %s: %w", what, err)
	}
	entry.Debug().Msgf("Finished %s", what)
	return nil
}

func (n *nmLinux) AddNfLog(ipNet *net.IPNet, port uint16, prefix string, isUdp bool) error {
	return n.updateVs("adding nflog", ipNet, port, isUdp, func(v *vsRules) {
		v.nflogPrefix = prefix
	})
}

func (n *nmLinux) DelNfLog(ipNet *net.IPNet, port uint16, prefix string, isUdp bool) error {
	return n.updateVs("deleting nflog", ipNet, port, isUdp, func(v *vsRules) {
		v.nflogPrefix = ""
	})
}

func (n *nmLinux) AddSnat(ipNet *net.IPNet, port uint16, isUdp bool, logged bool) error {
	mark := n.conf.SnatMark
	if logged {
		mark = n.conf.SnatLogMark
	}
	return n.updateVs("adding snat mark", ipNet, port, isUdp, func(v *vsRules) {
		v.mark = mark
	})
}

func (n *nmLinux) DelSnat(ipNet *net.IPNet, port uint16, isUdp bool, logged bool) error {
	return n.updateVs("deleting snat mark", ipNet, port, isUdp, clearSnatMark(n.conf))
}

func (n *nmLinux) AddMarkLog(ipNet *net.IPNet, port uint16, isUdp bool) error {
	return n.updateVs("adding log mark", ipNet, port, isUdp, func(v *vsRules) {
		v.mark = n.conf.LogOnlyMark
	})
}

func (n *nmLinux) DelMarkLog(ipNet *net.IPNet, port uint16, isUdp bool) error {
	return n.updateVs("deleting log mark", ipNet, port, isUdp, clearLogOnlyMark(n.conf))
}

func (n *nmLinux) AddConnLimit(ipNet *net.IPNet, port uint16, isUdp bool, limit uint64, withSrcAddr bool, withDestAddr bool) error {
	return n.updateVs("adding conn limit", ipNet, port, isUdp, func(v *vsRules) {
		v.connLimit = &connLimit{max: limit, bySource: withSrcAddr, byDestination: withDestAddr}
	})
}

func (n *nmLinux) DelConnLimit(ipNet *net.IPNet, port uint16, isUdp bool, limit uint64, withSrcAddr bool, withDestAddr bool) error {
	return n.updateVs("deleting conn limit", ipNet, port, isUdp, func(v *vsRules) {
		v.connLimit = nil
	})
}

func (n *nmLinux) AddConnRateLimit(ipNet *net.IPNet, port uint16, isUdp bool, rate string, burst uint64, withSrcAddr bool, withDestAddr bool) error {
	return n.updateVs("adding conn rate limit", ipNet, port, isUdp, func(v *vsRules) {
		v.rateLimit = &rateLimit{rate: rate, burst: burst, bySource: withSrcAddr, byDestination: withDestAddr}
	})
}

func (n *nmLinux) DelConnRateLimit(ipNet *net.IPNet, port uint16, isUdp bool, rate string, burst uint64, withSrcAddr bool, withDestAddr bool) error {
	return n.updateVs("deleting conn rate limit", ipNet, port, isUdp, func(v *vsRules) {
		v.rateLimit = nil
	})
}

// cleanup also deletes the dummy interface: its vips go with it, those removed while the agent was down included
func cleanup(conf config.NetworkManagerConfig) error {
	var result error
	nft, err := newNft()
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), nftTransactionTimeout)
		err = deleteTable(ctx, nft)
		cancel()
	}
	if err != nil {
		result = multierror.Append(result, fmt.Errorf("failed to delete the nftables table %s: %w", nftTable, err))
	}
	err = deleteDummyInterface(conf.DummyInterfaceName)
	if err != nil {
		result = multierror.Append(result, fmt.Errorf("failed to delete interface %s: %w", conf.DummyInterfaceName, err))
	}
	return result
}

// nftSnater source-nats the marked packets leaving each interface to its first ip:
// masquerade would look the address up for every connection
func nftSnater(conf config.NetworkManagerConfig, ifaces []string) error {
	var result error
	var sources []snatSource
	for _, ifaceName := range ifaces {
		ip, err := firstInterfaceIp(ifaceName)
		if err != nil {
			result = multierror.Append(result, err)
			continue
		}
		sources = append(sources, snatSource{iface: ifaceName, ip: ip})
	}

	nft, err := newNft()
	if err != nil {
		return multierror.Append(result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), nftTransactionTimeout)
	defer cancel()
	err = addSnatSources(ctx, nft, conf, sources)
	if err != nil {
		result = multierror.Append(result, fmt.Errorf("failed to add the snat rules: %w", err))
	}
	return result
}

func firstInterfaceIp(ifaceName string) (net.IP, error) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return nil, fmt.Errorf("failed to get interface %s: %w", ifaceName, err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to get addresses of interface %s: %w", ifaceName, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("interface %s has no addresses", ifaceName)
	}
	switch v := addrs[0].(type) {
	case *net.IPNet:
		return v.IP, nil
	case *net.IPAddr:
		return v.IP, nil
	}
	return nil, fmt.Errorf("interface %s has an address of unknown type %T", ifaceName, addrs[0])
}
