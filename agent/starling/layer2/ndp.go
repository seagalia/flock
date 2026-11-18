// licensed to metallb authors under the apache license, version 2.0
// file: https://github.com/metallb/metallb/tree/main/internal/layer2/ndp.go

package layer2

import (
	"fmt"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"io"
	"net"
	"net/netip"

	"github.com/mdlayher/ndp"
)

type ndpResponder struct {
	logger       zerolog.Logger
	intf         string
	hardwareAddr net.HardwareAddr
	conn         *ndp.Conn
	closed       chan struct{}
	announce     announceFunc
	// Refcount of how many watchers for each solicited node
	// multicast group.
	solicitedNodeGroups map[string]int64
}

func newNDPResponder(ifi *net.Interface, ann announceFunc) (*ndpResponder, error) {
	// Use link-local address as the source IPv6 address for NDP communications.
	conn, _, err := ndp.Listen(ifi, ndp.LinkLocal)
	if err != nil {
		return nil, fmt.Errorf("creating NDP responder for %q: %s", ifi.Name, err)
	}

	ret := &ndpResponder{
		logger:              log.Logger.With().Str("interface", ifi.Name).Str("component", "ndp_responder").Logger(),
		intf:                ifi.Name,
		hardwareAddr:        ifi.HardwareAddr,
		conn:                conn,
		closed:              make(chan struct{}),
		announce:            ann,
		solicitedNodeGroups: map[string]int64{},
	}
	go ret.run()
	return ret, nil
}

func (n *ndpResponder) Interface() string { return n.intf }

func (n *ndpResponder) Close() error {
	close(n.closed)
	return n.conn.Close()
}

func (n *ndpResponder) Gratuitous(ip netip.Addr) error {

	linkLocal, _ := netip.AddrFromSlice(net.IPv6linklocalallnodes) // nolint
	err := n.advertise(linkLocal, ip, true)
	stats.SentGratuitous(ip.String())
	return err
}

func (n *ndpResponder) Watch(ip netip.Addr) error {
	if ip.Is4() {
		return nil
	}
	group, err := ndp.SolicitedNodeMulticast(ip)
	if err != nil {
		return fmt.Errorf("looking up solicited node multicast group for %q: %s", ip, err)
	}
	if n.solicitedNodeGroups[group.String()] == 0 {
		if err = n.conn.JoinGroup(group); err != nil {
			return fmt.Errorf("joining solicited node multicast group for %q: %s", ip, err)
		}
	}
	n.solicitedNodeGroups[group.String()]++
	return nil
}

func (n *ndpResponder) Unwatch(ip netip.Addr) error {
	if ip.Is4() {
		return nil
	}
	group, err := ndp.SolicitedNodeMulticast(ip)
	if err != nil {
		return fmt.Errorf("looking up solicited node multicast group for %q: %s", ip, err)
	}
	n.solicitedNodeGroups[group.String()]--
	if n.solicitedNodeGroups[group.String()] == 0 {
		if err = n.conn.LeaveGroup(group); err != nil {
			return fmt.Errorf("leaving solicited node multicast group for %q: %s", ip, err)
		}
	}
	return nil
}

func (n *ndpResponder) run() {
	for n.processRequest() != dropReasonClosed {
	}
}

func (n *ndpResponder) processRequest() dropReason {
	msg, _, src, err := n.conn.ReadFrom()
	if err != nil {
		select {
		case <-n.closed:
			return dropReasonClosed
		default:
		}
		if err == io.EOF {
			return dropReasonClosed
		}
		return dropReasonError
	}

	ns, ok := msg.(*ndp.NeighborSolicitation)
	if !ok {
		return dropReasonMessageType
	}

	var nsLLAddr net.HardwareAddr
	for _, o := range ns.Options {
		// Ignore other options, including target link-layer address instead of source.
		lla, ok := o.(*ndp.LinkLayerAddress)
		if !ok {
			continue
		}
		if lla.Direction != ndp.Source {
			continue
		}

		nsLLAddr = lla.Addr
		break
	}
	if nsLLAddr == nil {
		return dropReasonNoSourceLL
	}

	// Ignore NDP requests that the announcer tells us to ignore.
	reason := n.announce(ns.TargetAddress, n.intf)
	if reason == dropReasonNotMatchInterface {
		n.logger.Debug().
			Str("op", "ndpRequestIgnore").
			Stringer("ip", ns.TargetAddress).
			Msg("Reason: not match interface")
	}
	if reason != dropReasonNone {
		return reason
	}

	stats.GotRequest(ns.TargetAddress.String())
	n.logger.Debug().
		Stringer("ip", ns.TargetAddress).
		Stringer("senderIP", src).
		Stringer("senderLLAddr", nsLLAddr).
		Stringer("responseMAC", n.hardwareAddr).
		Msg("got NDP request for service IP, sending response")

	if err := n.advertise(src, ns.TargetAddress, false); err != nil {
		n.logger.Error().
			Err(err).
			Str("op", "ndpReply").
			Stringer("ip", ns.TargetAddress).
			Stringer("senderIP", src).
			Stringer("senderLLAddr", nsLLAddr).
			Stringer("responseMAC", n.hardwareAddr).
			Msg("failed to send NDP reply")
	} else {
		stats.SentResponse(ns.TargetAddress.String())
	}
	return dropReasonNone
}

func (n *ndpResponder) advertise(dst, target netip.Addr, gratuitous bool) error {
	m := &ndp.NeighborAdvertisement{
		Solicited:     !gratuitous,
		Override:      gratuitous, // Should clients replace existing cache entries
		TargetAddress: target,
		Options: []ndp.Option{
			&ndp.LinkLayerAddress{
				Direction: ndp.Target,
				Addr:      n.hardwareAddr,
			},
		},
	}
	return n.conn.WriteTo(m, nil, dst)
}
