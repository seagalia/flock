// licensed to metallb authors under the apache license, version 2.0
// file: https://github.com/metallb/metallb/tree/main/internal/layer2/arp.go

package layer2

import (
	"bytes"
	"fmt"
	"github.com/mdlayher/arp"
	"github.com/mdlayher/ethernet"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/logging"
	"io"
	"net"
	"net/netip"
)

type announceFunc func(netip.Addr, string) dropReason

type arpResponder struct {
	logger       zerolog.Logger
	intf         string
	hardwareAddr net.HardwareAddr
	conn         *arp.Client
	closed       chan struct{}
	announce     announceFunc
}

func newARPResponder(ifi *net.Interface, ann announceFunc) (*arpResponder, error) {
	client, err := arp.Dial(ifi)
	if err != nil {
		return nil, fmt.Errorf("creating ARP responder for %q: %s", ifi.Name, err)
	}

	ret := &arpResponder{
		logger:       logging.Component("arp_responder").With().Str("interface", ifi.Name).Logger(),
		intf:         ifi.Name,
		hardwareAddr: ifi.HardwareAddr,
		conn:         client,
		closed:       make(chan struct{}),
		announce:     ann,
	}
	go ret.run()
	return ret, nil
}

func (a *arpResponder) Interface() string { return a.intf }

func (a *arpResponder) Close() error {
	close(a.closed)
	return a.conn.Close()
}

func (a *arpResponder) Gratuitous(ip netip.Addr) error {
	for _, op := range []arp.Operation{arp.OperationRequest, arp.OperationReply} {
		pkt, err := arp.NewPacket(op, a.hardwareAddr, ip, ethernet.Broadcast, ip)
		if err != nil {
			return fmt.Errorf("assembling %q gratuitous packet for %q: %s", op, ip, err)
		}
		if err = a.conn.WriteTo(pkt, ethernet.Broadcast); err != nil {
			return fmt.Errorf("writing %q gratuitous packet for %q: %s", op, ip, err)
		}
		stats.SentGratuitous(ip.String())
	}
	return nil
}

func (a *arpResponder) run() {
	for a.processRequest() != dropReasonClosed {
	}
}

func (a *arpResponder) processRequest() dropReason {
	pkt, eth, err := a.conn.Read()
	if err != nil {
		a.logger.Error().Err(err).Msg("ARP responder closed")
		// ARP listener doesn't cleanly return EOF when closed, so we
		// need to hook into the call to arpResponder.Close()
		// independently.
		select {
		case <-a.closed:
			return dropReasonClosed
		default:
		}
		if err == io.EOF {
			return dropReasonClosed
		}
		return dropReasonError
	}
	// Ignore ARP replies.
	if pkt.Operation != arp.OperationRequest {
		return dropReasonARPReply
	}

	// Ignore ARP requests which are not broadcast or bound directly for this machine.
	if !bytes.Equal(eth.Destination, ethernet.Broadcast) && !bytes.Equal(eth.Destination, a.hardwareAddr) {
		return dropReasonEthernetDestination
	}

	// Ignore ARP requests that the announcer tells us to ignore.
	reason := a.announce(pkt.TargetIP, a.intf)
	if reason == dropReasonNotMatchInterface {
		a.logger.Debug().
			Str("op", "arpRequestIgnore").
			Stringer("ip", pkt.TargetIP).
			Msg("Reason: not match interface")
	}
	if reason != dropReasonNone {
		return reason
	}

	stats.GotRequest(pkt.TargetIP.String())
	a.logger.Debug().
		Stringer("ip", pkt.TargetIP).
		Stringer("senderIP", pkt.SenderIP).
		Stringer("senderMAC", pkt.SenderHardwareAddr).
		Stringer("responseMAC", a.hardwareAddr).
		Msg("got ARP request for service IP, sending response")
	if err := a.conn.Reply(pkt, a.hardwareAddr, pkt.TargetIP); err != nil {
		a.logger.Error().
			Err(err).
			Str("op", "arpReply").
			Stringer("ip", pkt.TargetIP).
			Stringer("senderIP", pkt.SenderIP).
			Stringer("senderMAC", pkt.SenderHardwareAddr).
			Stringer("responseMAC", a.hardwareAddr).
			Msg("failed to send ARP reply")
	} else {
		stats.SentResponse(pkt.TargetIP.String())
	}
	return dropReasonNone
}
