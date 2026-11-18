package bgp

import (
	"context"
	"time"

	apipb "github.com/osrg/gobgp/v4/api"
	"github.com/seagalia/flock/agent/starling/config"
)

// AddPeer will add peers to the BGP configuration
func (b *Server) AddPeer(ctx context.Context, peer *config.BGPPeer) (err error) {
	keepAlive := uint64(0)
	if peer.KeepaliveInterval != 0 {
		keepAlive = uint64(time.Duration(peer.KeepaliveInterval).Seconds())
	}
	holdTime := uint64(0)
	if peer.HoldTime != 0 {
		holdTime = uint64(time.Duration(peer.HoldTime).Seconds())
	}
	connectRetry := uint64(0)
	if peer.ConnectRetry != 0 {
		connectRetry = uint64(time.Duration(peer.ConnectRetry).Seconds())
	}
	p := &apipb.Peer{
		Conf: &apipb.PeerConf{
			AuthPassword:    peer.Password,
			Description:     peer.Name,
			LocalAsn:        peer.MyASN,
			NeighborAddress: peer.Address,
			PeerAsn:         peer.PeerAsn,
			Vrf:             peer.VRF,
		},

		Timers: &apipb.Timers{
			Config: &apipb.TimersConfig{
				ConnectRetry:      connectRetry,
				HoldTime:          holdTime,
				KeepaliveInterval: keepAlive,
			},
		},

		// This enables routes to be sent to routers across multiple hops
		EbgpMultihop: &apipb.EbgpMultihop{
			Enabled:     peer.EBGPMultiHop,
			MultihopTtl: 50,
		},
		Transport: &apipb.Transport{
			MtuDiscovery:  true,
			RemoteAddress: peer.Address,
			RemotePort:    uint32(peer.Port),
		},
		// tells the peer we are capable of unicast IPv4 and IPv6
		// advertisements.
		AfiSafis: []*apipb.AfiSafi{
			{
				Config: &apipb.AfiSafiConfig{
					Family: &apipb.Family{
						Afi:  apipb.Family_AFI_IP,
						Safi: apipb.Family_SAFI_UNICAST,
					},
				},
			},
			{
				Config: &apipb.AfiSafiConfig{
					Family: &apipb.Family{
						Afi:  apipb.Family_AFI_IP6,
						Safi: apipb.Family_SAFI_UNICAST,
					},
				},
			},
		},
	}

	if peer.SrcAddr != "" {
		p.Transport.LocalAddress = peer.SrcAddr
	}

	if peer.SrcInterface != "" {
		p.Transport.BindInterface = peer.SrcInterface
	}

	if peer.Bfd != nil {
		p.Bfd = &apipb.BfdPeerConfig{
			Enabled:                  peer.Bfd.Enabled,
			Port:                     peer.Bfd.Port,
			DesiredMinimumTxInterval: uint32(time.Duration(peer.Bfd.DesiredMinimumTxInterval).Microseconds()),
			RequiredMinimumReceive:   uint32(time.Duration(peer.Bfd.RequiredMinimumReceive).Microseconds()),
			DetectionMultiplier:      peer.Bfd.DetectionMultiplier,
		}
	}

	return b.bgpServ.AddPeer(ctx, &apipb.AddPeerRequest{
		Peer: p,
	})
}
