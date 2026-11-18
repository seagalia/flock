package bgp

import (
	"bytes"
	"context"
	"net"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apipb "github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
	gobgp "github.com/osrg/gobgp/v4/pkg/server"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"

	"github.com/seagalia/flock/agent/starling/config"
)

// peerHoldTime is the smallest hold time gobgp accepts, in seconds
const peerHoldTime = 3

func cidr(raw string) *config.CIDR {
	_, ipNet, err := net.ParseCIDR(raw)
	Expect(err).ToNot(HaveOccurred())
	return &config.CIDR{IPNet: ipNet}
}

func community(asn, value uint32) *config.BGPCommunity {
	return &config.BGPCommunity{Asn: asn, Value: value}
}

func findAttr[T bgp.PathAttributeInterface](attrs []bgp.PathAttributeInterface) (T, bool) {
	for _, attr := range attrs {
		if typed, ok := attr.(T); ok {
			return typed, true
		}
	}
	var zero T
	return zero, false
}

func listPrefixes(server *gobgp.BgpServer, family bgp.Family) []string {
	return listTablePrefixes(server, apipb.TableType_TABLE_TYPE_GLOBAL, "", family)
}

// listAdjOutPrefixes is what is sent to the 127.0.0.1 neighbor
func listAdjOutPrefixes(server *gobgp.BgpServer, family bgp.Family) []string {
	return listTablePrefixes(server, apipb.TableType_TABLE_TYPE_ADJ_OUT, "127.0.0.1", family)
}

func listTablePrefixes(server *gobgp.BgpServer, tableType apipb.TableType, name string, family bgp.Family) []string {
	prefixes := make([]string, 0)
	request := apiutil.ListPathRequest{TableType: tableType, Name: name, Family: family}
	err := server.ListPath(request, func(prefix bgp.NLRI, _ []*apiutil.Path) {
		prefixes = append(prefixes, prefix.String())
	})
	Expect(err).ToNot(HaveOccurred())
	slices.Sort(prefixes)
	return prefixes
}

var _ = Describe("Bgp server", func() {
	Context("makeRouterId", func() {
		It("should take the highest source address comparing ips numerically", func() {
			routerId, err := makeRouterId([]*config.BGPPeer{
				{SrcAddr: "9.0.0.1"},
				{SrcAddr: "10.0.0.2"},
				{SrcAddr: "10.0.0.1"},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(routerId).To(Equal("10.0.0.2"))
		})

		It("should prefer ipv4 over ipv6", func() {
			routerId, err := makeRouterId([]*config.BGPPeer{
				{SrcAddr: "2001:db8::ffff"},
				{SrcAddr: "10.0.0.1"},
				{SrcAddr: "2001:db8::fffe"},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(routerId).To(Equal("10.0.0.1"))
		})

		It("should use highest ipv4 address of source interface", func() {
			loIface := loopbackInterface()
			iface, err := net.InterfaceByName(loIface)
			Expect(err).ToNot(HaveOccurred())
			addrs, err := iface.Addrs()
			Expect(err).ToNot(HaveOccurred())
			var expected net.IP
			for _, addr := range addrs {
				ip, _, err := net.ParseCIDR(addr.String())
				Expect(err).ToNot(HaveOccurred())
				if ip.To4() != nil && (expected == nil || bytes.Compare(ip.To16(), expected.To16()) > 0) {
					expected = ip
				}
			}

			routerId, err := makeRouterId([]*config.BGPPeer{{SrcInterface: loIface}})
			Expect(err).ToNot(HaveOccurred())
			Expect(routerId).To(Equal(expected.String()))
		})

		It("should fail on unknown interface", func() {
			_, err := makeRouterId([]*config.BGPPeer{{SrcInterface: "this-iface-does-not-exist"}})
			Expect(err).To(MatchError(ContainSubstring("failed to get interface")))
		})

		It("should fail when no address can be found", func() {
			_, err := makeRouterId([]*config.BGPPeer{})
			Expect(err).To(MatchError(ContainSubstring("no ip address found")))
		})
	})

	Context("getPath", func() {
		var server *Server

		BeforeEach(func() {
			server = NewServer(65000, "10.0.0.1", nil, []*config.BGPAdvConfig{
				{LocalPref: 100, Communities: []*config.BGPCommunity{community(65000, 1)}},
				{LocalPref: 200, Communities: []*config.BGPCommunity{community(65000, 2)}, CIDRs: []*config.CIDR{cidr("10.1.0.0/16")}},
				{LocalPref: 300, CIDRs: []*config.CIDR{cidr("2001:db8::/32")}},
			})
		})

		It("should find advertisements matching vip", func() {
			Expect(server.findAdvs(&vip.VIP{Ip: "192.168.0.1"})).To(HaveLen(1))
			Expect(server.findAdvs(&vip.VIP{Ip: "10.1.0.1"})).To(HaveLen(2))
			Expect(server.findAdvs(&vip.VIP{Ip: "2001:db8::1"})).To(HaveLen(2))
		})

		It("should build an ipv4 path with merged communities and highest local pref", func() {
			path, err := server.getPath(&vip.VIP{Ip: "10.1.0.1"})
			Expect(err).ToNot(HaveOccurred())
			Expect(path.Family).To(Equal(bgp.RF_IPv4_UC))
			Expect(path.Nlri.String()).To(Equal("10.1.0.1/32"))

			comms, found := findAttr[*bgp.PathAttributeCommunities](path.Attrs)
			Expect(found).To(BeTrue())
			Expect(comms.Value).To(Equal([]uint32{65000<<16 | 1, 65000<<16 | 2}))

			localPref, found := findAttr[*bgp.PathAttributeLocalPref](path.Attrs)
			Expect(found).To(BeTrue())
			Expect(localPref.Value).To(Equal(uint32(200)))

			_, found = findAttr[*bgp.PathAttributeNextHop](path.Attrs)
			Expect(found).To(BeTrue())
		})

		It("should keep vip prefix length", func() {
			path, err := server.getPath(&vip.VIP{Ip: "10.2.0.0", PrefixLength: 24})
			Expect(err).ToNot(HaveOccurred())
			Expect(path.Nlri.String()).To(Equal("10.2.0.0/24"))
		})

		It("should build an ipv6 path with mp reach attribute", func() {
			path, err := server.getPath(&vip.VIP{Ip: "2001:db8::1"})
			Expect(err).ToNot(HaveOccurred())
			Expect(path.Family).To(Equal(bgp.RF_IPv6_UC))
			Expect(path.Nlri.String()).To(Equal("2001:db8::1/128"))
			mpReach, found := findAttr[*bgp.PathAttributeMpReachNLRI](path.Attrs)
			Expect(found).To(BeTrue())
			Expect(mpReach.Value).To(HaveLen(1))
			localPref, _ := findAttr[*bgp.PathAttributeLocalPref](path.Attrs)
			Expect(localPref.Value).To(Equal(uint32(300)))
		})

		It("should not add optional attributes without advertisements", func() {
			server = NewServer(65000, "10.0.0.1", nil, nil)
			path, err := server.getPath(&vip.VIP{Ip: "10.0.0.5"})
			Expect(err).ToNot(HaveOccurred())
			_, found := findAttr[*bgp.PathAttributeCommunities](path.Attrs)
			Expect(found).To(BeFalse())
			_, found = findAttr[*bgp.PathAttributeLocalPref](path.Attrs)
			Expect(found).To(BeFalse())
		})
	})

	Context("announcing to a peer", func() {
		var (
			ctx       context.Context
			cancel    context.CancelFunc
			peerSpeak *gobgp.BgpServer
			server    *Server
			runDone   chan error
			stopped   bool
		)

		BeforeEach(func() {
			ctx, cancel = context.WithCancel(context.Background())
			stopped = false
			peerPort := freeTcpPort()

			// router side, passively waiting for the agent
			peerSpeak = gobgp.NewBgpServer()
			go peerSpeak.Serve()
			Expect(peerSpeak.StartBgp(ctx, &apipb.StartBgpRequest{Global: &apipb.Global{
				Asn:             65001,
				RouterId:        "10.255.0.1",
				ListenPort:      int32(peerPort),
				ListenAddresses: []string{"127.0.0.1"},
			}})).To(Succeed())
			Expect(peerSpeak.AddPeer(ctx, &apipb.AddPeerRequest{Peer: &apipb.Peer{
				Conf:      &apipb.PeerConf{NeighborAddress: "127.0.0.1", PeerAsn: 65000},
				Transport: &apipb.Transport{PassiveMode: true},
				// the agent sets no hold time, the session negotiates this one and the stop waits it
				Timers: &apipb.Timers{Config: &apipb.TimersConfig{HoldTime: peerHoldTime, KeepaliveInterval: 1}},
				AfiSafis: []*apipb.AfiSafi{
					{Config: &apipb.AfiSafiConfig{Family: &apipb.Family{Afi: apipb.Family_AFI_IP, Safi: apipb.Family_SAFI_UNICAST}}},
					{Config: &apipb.AfiSafiConfig{Family: &apipb.Family{Afi: apipb.Family_AFI_IP6, Safi: apipb.Family_SAFI_UNICAST}}},
				},
			}})).To(Succeed())

			server = NewServer(65000, "", []*config.BGPPeer{{
				Name:    "router",
				PeerAsn: 65001,
				Address: "127.0.0.1",
				Port:    peerPort,
				SrcAddr: "127.0.0.1",
			}}, []*config.BGPAdvConfig{{LocalPref: 150, Communities: []*config.BGPCommunity{community(65000, 42)}}})

			ready := make(chan struct{})
			runDone = make(chan error, 1)
			go func() {
				runDone <- server.Run(ctx, func() { close(ready) })
			}()
			Eventually(ready, 10*time.Second).Should(BeClosed())

			Eventually(func() apipb.PeerState_SessionState {
				state := apipb.PeerState_SESSION_STATE_UNSPECIFIED
				_ = peerSpeak.ListPeer(ctx, &apipb.ListPeerRequest{}, func(p *apipb.Peer) {
					state = p.GetState().GetSessionState()
				})
				return state
			}, 30*time.Second).Should(Equal(apipb.PeerState_SESSION_STATE_ESTABLISHED))
		})

		AfterEach(func() {
			cancel()
			if !stopped {
				Eventually(runDone, 10*time.Second).Should(Receive(BeNil()))
			}
			Expect(peerSpeak.StopBgp(context.Background(), &apipb.StopBgpRequest{})).To(Succeed())
		})

		It("should announce and withdraw vips to the peer", func() {
			v4 := &vip.VIP{UUID: "vip-4", Ip: "10.0.0.10"}
			v6 := &vip.VIP{UUID: "vip-6", Ip: "2001:db8::10"}
			Expect(server.AddVip(ctx, v4)).To(Succeed())
			Expect(server.AddVip(ctx, v6)).To(Succeed())

			// adj-rib-out is checked as the peer rejects routes with a loopback next hop
			Eventually(func() []string { return listAdjOutPrefixes(server.bgpServ, bgp.RF_IPv4_UC) }, 10*time.Second).
				Should(Equal([]string{"10.0.0.10/32"}))
			Eventually(func() []string { return listAdjOutPrefixes(server.bgpServ, bgp.RF_IPv6_UC) }, 10*time.Second).
				Should(Equal([]string{"2001:db8::10/128"}))

			var sentComms []uint32
			Expect(server.bgpServ.ListPath(apiutil.ListPathRequest{
				TableType: apipb.TableType_TABLE_TYPE_ADJ_OUT,
				Name:      "127.0.0.1",
				Family:    bgp.RF_IPv4_UC,
			}, func(_ bgp.NLRI, paths []*apiutil.Path) {
				comms, _ := findAttr[*bgp.PathAttributeCommunities](paths[0].Attrs)
				sentComms = comms.Value
			})).To(Succeed())
			Expect(sentComms).To(Equal([]uint32{65000<<16 | 42}))

			Expect(server.DelVip(ctx, v4)).To(Succeed())
			Eventually(func() []string { return listAdjOutPrefixes(server.bgpServ, bgp.RF_IPv4_UC) }, 10*time.Second).Should(BeEmpty())
			Expect(listAdjOutPrefixes(server.bgpServ, bgp.RF_IPv6_UC)).To(HaveLen(1))
		})

		It("should report the peer session up then down when the peer drops it", func() {
			session := stats.peers.WithLabelValues("127.0.0.1", "router")
			Eventually(func() float64 { return testutil.ToFloat64(session) }, 10*time.Second).Should(Equal(1.0))

			Expect(peerSpeak.DeletePeer(ctx, &apipb.DeletePeerRequest{Address: "127.0.0.1"})).To(Succeed())
			Eventually(func() float64 { return testutil.ToFloat64(session) }, 30*time.Second).Should(Equal(0.0))
		})

		It("should wait the hold time negotiated with the peer when stopping", func() {
			stopAsked := time.Now()
			cancel()
			Eventually(runDone, 10*time.Second).Should(Receive(BeNil()))
			stopped = true

			Expect(time.Since(stopAsked)).To(BeNumerically(">=", peerHoldTime*time.Second))
		})

		It("should announce a vip once even if added multiple times", func() {
			aVip := &vip.VIP{UUID: "vip-4", Ip: "10.0.0.10"}
			Expect(server.AddVip(ctx, aVip)).To(Succeed())
			Expect(server.AddVip(ctx, aVip)).To(Succeed())
			Eventually(func() []string { return listPrefixes(server.bgpServ, bgp.RF_IPv4_UC) }).Should(HaveLen(1))

			// deleting an unknown vip is a no-op
			Expect(server.DelVip(ctx, &vip.VIP{UUID: "unknown", Ip: "10.9.9.9"})).To(Succeed())
		})
	})
})

var _ = Describe("Server start failure", func() {
	It("should stop the management loop of gobgp when it fails to start", func() {
		// gobgp refuses a router id that isn't ipv4
		server := NewServer(65000, "::1", nil, nil)

		Expect(server.Run(context.Background(), nil)).To(MatchError(ContainSubstring("failed to start bgp server")))

		// a management call reaches a running loop, and fails once the loop stopped
		Eventually(func() error {
			_, err := server.bgpServ.GetBgp(context.Background(), &apipb.GetBgpRequest{})
			return err
		}).Should(MatchError(ContainSubstring("server stopped")))
	})
})
