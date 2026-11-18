package bgp

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	apipb "github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
	gobgp "github.com/osrg/gobgp/v4/pkg/server"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/sdk/logging"
)

const (
	// stopTimeout is the ctx of StopBgp. gobgp v4 doesn't watch it and waits for its sessions to end, so only the stop
	// budget of the app bounds a stuck stop.
	stopTimeout = 1 * time.Minute
	// a peer may negotiate a hold time up to 18h, the wait must leave the stop budget of the app to the other parts
	maxDrain = 5 * time.Minute
)

type Server struct {
	localAsn uint32
	entry    zerolog.Logger
	peers    []*config.BGPPeer
	advs     []*config.BGPAdvConfig
	bgpServ  *gobgp.BgpServer
	refVips  *sync.Map
	routerId string
}

func NewServer(localAsn uint32, routerId string, peers []*config.BGPPeer, advs []*config.BGPAdvConfig) *Server {
	server := &Server{
		entry:    logging.Component("bgp"),
		peers:    peers,
		advs:     advs,
		routerId: routerId,
		localAsn: localAsn,
		refVips:  &sync.Map{},
		bgpServ:  gobgp.NewBgpServer(gobgp.LoggerOption(newBgpLogger(), nil)),
	}
	return server
}

func (b *Server) Run(ctx context.Context, afterInit func()) error {
	routerId := b.routerId
	var err error
	if routerId == "" {
		routerId, err = makeRouterId(b.peers)
		if err != nil {
			return fmt.Errorf("failed to make router id: %w", err)
		}
	}

	b.entry = b.entry.With().Str("router-id", routerId).Logger()
	b.entry.Info().Msgf("Starting BGP server with router id %s", routerId)
	// gobgp's management loop, StopBgp ends it
	go b.bgpServ.Serve()
	err = b.start(ctx, routerId)
	if err != nil {
		stopErr := b.stop()
		if stopErr != nil {
			b.entry.Warn().Err(stopErr).Msg("failed to stop the BGP server after its start failed")
		}
		return err
	}
	if afterInit != nil {
		afterInit()
	}
	<-ctx.Done()
	return b.Close()
}

// start needs the management loop of gobgp running, StartBgp is a request answered by it
func (b *Server) start(ctx context.Context, routerId string) error {
	err := b.bgpServ.StartBgp(ctx, &apipb.StartBgpRequest{
		Global: &apipb.Global{
			Asn:             b.localAsn,
			RouterId:        routerId,
			ListenPort:      -1,
			GracefulRestart: &apipb.GracefulRestart{Enabled: true},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to start bgp server: %w", err)
	}
	watchPeerStates := gobgp.WatchEventMessageCallbacks{
		OnPeerUpdate: func(event *apiutil.WatchEventMessage_PeerEvent, _ time.Time) {
			if event.Type != apiutil.PEER_EVENT_STATE {
				return
			}
			b.entry.Info().Msg(fmt.Sprint(event.Peer))
			b.recordPeerState(event.Peer.State)
		},
	}
	if err = b.bgpServ.WatchEvent(ctx, watchPeerStates, gobgp.WatchPeer()); err != nil {
		return fmt.Errorf("failed to watch bgp events: %w", err)
	}

	for _, peer := range b.peers {
		// a peer that never connects sends no state event, it must still show as down
		stats.SetPeerSession(peer, false)
		if err := b.AddPeer(ctx, peer); err != nil {
			return fmt.Errorf("failed to add peer '%s': %w", peer.Name, err)
		}
	}
	return nil
}

// stop ends the sessions and the management loop of gobgp, with no bound of its own (see stopTimeout)
func (b *Server) stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	err := b.bgpServ.StopBgp(ctx, &apipb.StopBgpRequest{})
	if err != nil {
		return fmt.Errorf("failed to stop bgp server: %w", err)
	}
	return nil
}

func (b *Server) recordPeerState(state apiutil.PeerState) {
	for _, peer := range b.peers {
		// gobgp gives the address in canonical form, the config may not
		configured, err := netip.ParseAddr(peer.Address)
		if err != nil || configured != state.NeighborAddress {
			continue
		}
		stats.SetPeerSession(peer, state.SessionState == bgp.BGP_FSM_ESTABLISHED)
		return
	}
}

// Close stops a running BGP server, then waits the biggest hold time negotiated with the peers.
func (b *Server) Close() error {
	drain := min(b.biggestNegotiatedHoldTime(), maxDrain)
	b.entry.Info().Msg("Stopping BGP server...")
	err := b.stop()
	if err != nil {
		return err
	}
	b.entry.Info().Msgf("Waiting %s for the peers to drop the routes of this server ...", drain)
	time.Sleep(drain)
	b.entry.Info().Msg("Finished ensuring.")
	return nil
}

// biggestNegotiatedHoldTime is read before the stop, which removes the peers.
func (b *Server) biggestNegotiatedHoldTime() time.Duration {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	biggest := time.Duration(0)
	err := b.bgpServ.ListPeer(ctx, &apipb.ListPeerRequest{}, func(peer *apipb.Peer) {
		holdTime := time.Duration(peer.GetTimers().GetState().GetNegotiatedHoldTime()) * time.Second
		biggest = max(biggest, holdTime)
	})
	if err != nil {
		b.entry.Warn().Err(err).Msg("failed to read the hold times of the peers, stopping with no wait")
	}
	return biggest
}

// makeRouterId prefers ipv4 as a router id is usually an ipv4
func makeRouterId(peers []*config.BGPPeer) (string, error) {
	var higherIp net.IP
	consider := func(ip net.IP) {
		if ip == nil {
			return
		}
		if higherIp == nil {
			higherIp = ip
			return
		}
		isV4 := ip.To4() != nil
		higherIsV4 := higherIp.To4() != nil
		if isV4 != higherIsV4 {
			if isV4 {
				higherIp = ip
			}
			return
		}
		if bytes.Compare(ip.To16(), higherIp.To16()) > 0 {
			higherIp = ip
		}
	}
	for _, peer := range peers {
		if peer.SrcAddr != "" {
			consider(net.ParseIP(peer.SrcAddr))
			continue
		}
		iface, err := net.InterfaceByName(peer.SrcInterface)
		if err != nil {
			return "", fmt.Errorf("failed to get interface '%s': %w", peer.SrcInterface, err)
		}
		addrs, err := iface.Addrs()
		if err != nil {
			return "", fmt.Errorf("failed to get addresses from interface '%s': %w", peer.SrcInterface, err)
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				ip = net.ParseIP(addr.String())
			}
			consider(ip)
		}
	}
	if higherIp == nil {
		return "", fmt.Errorf("no ip address found in peers to make a router id")
	}
	return higherIp.String(), nil
}
