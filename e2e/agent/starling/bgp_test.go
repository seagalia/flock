//go:build e2e

package e2e_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apipb "github.com/osrg/gobgp/v4/api"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/helpers"

	"github.com/seagalia/flock/agent/starling/config"
	sdkconfig "github.com/seagalia/flock/sdk/config"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("BGP", Ordered, func() {
	// startBgpAgent starts an agent peering with a gobgp router, bfd is on for both or none of them
	startBgpAgent := func(ctx SpecContext, instance, agentIP, peerIP string, bfd *config.BfdConfig) (*env.GobgpPeer, *env.Agent) {
		peer, err := e2eEnv.StartGobgpPeer(ctx, peerIP, agentIP, 65000, 65001, bfd != nil)
		Expect(err).ToNot(HaveOccurred())
		agent, err := e2eEnv.StartAgent(ctx, env.AgentOptions{
			Name: "agent-" + instance, IP: agentIP, Instance: instance,
			Configure: func(conf *config.Config) {
				conf.AdvertisementConfig.BgpConfig = &config.BGPConfig{
					LocalAsn: 65000,
					RouterId: agentIP,
					Peers: []*config.BGPPeer{{
						Name: "router", PeerAsn: 65001, Address: peerIP, SrcAddr: agentIP,
						// a session dropped by the peer comes back fast
						ConnectRetry: sdkconfig.Duration(5 * time.Second),
						Bfd:          bfd,
					}},
					Advertisements: []*config.BGPAdvConfig{{
						Communities: []*config.BGPCommunity{{Asn: 65000, Value: 42}},
					}},
				}
			},
		})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) {
			e2eEnv.Controller.RemoveAll(instance)
			_ = agent.Resume(ctx)
			_ = agent.Stop(ctx)
		})
		return peer, agent
	}

	Context("without bfd", func() {
		const (
			instance = "bgp"
			agentIP  = "172.31.250.104"
			peerIP   = "172.31.250.110"
			vipIP    = "10.250.0.100"
		)
		var peer *env.GobgpPeer
		var agent *env.Agent
		BeforeAll(func(ctx SpecContext) {
			peer, agent = startBgpAgent(ctx, instance, agentIP, peerIP, nil)
		}, NodeTimeout(5*time.Minute))

		It("announces and withdraws a vip", func(ctx SpecContext) {
			e2eEnv.Controller.Push(instance, res.Vip("vip-bgp", vipIP, starling_announce.StarlingAnnounce_BGP))
			Eventually(func() ([]env.BgpRoute, error) { return peer.Rib(ctx) }, "1m").Should(ContainElement(env.BgpRoute{
				Prefix:      vipIP + "/32",
				NextHop:     agentIP,
				Communities: []string{"65000:42"},
			}))

			e2eEnv.Controller.Remove(instance, helpers.TypeUrl[*vip.VIP](), "vip-bgp")
			Eventually(func() ([]env.BgpRoute, error) { return peer.Rib(ctx) }).Should(BeEmpty())
		})

		It("keeps the routes of a hung agent until the hold time", func(ctx SpecContext) {
			e2eEnv.Controller.Push(instance, res.Vip("vip-bgp", vipIP, starling_announce.StarlingAnnounce_BGP))
			Eventually(func() ([]env.BgpRoute, error) { return peer.Rib(ctx) }, "1m").ShouldNot(BeEmpty())

			Expect(agent.Freeze(ctx)).To(Succeed())
			DeferCleanup(agent.Resume)

			// the hold time is 90s: without bfd nothing tells the router sooner
			Consistently(func() ([]env.BgpRoute, error) { return peer.Rib(ctx) }, "10s").ShouldNot(BeEmpty())
		}, NodeTimeout(2*time.Minute))
	})

	Context("with bfd", func() {
		const (
			instance = "bgp-bfd"
			agentIP  = "172.31.250.105"
			peerIP   = "172.31.250.111"
			vipIP    = "10.250.0.101"
		)
		var peer *env.GobgpPeer
		var agent *env.Agent
		bfdSessionState := func(ctx SpecContext) func() (apipb.BfdSessionState, error) {
			return func() (apipb.BfdSessionState, error) { return peer.BfdSessionState(ctx, agentIP) }
		}
		BeforeAll(func(ctx SpecContext) {
			peer, agent = startBgpAgent(ctx, instance, agentIP, peerIP, &config.BfdConfig{
				Enabled:                  true,
				Port:                     3784,
				DesiredMinimumTxInterval: sdkconfig.Duration(300 * time.Millisecond),
				RequiredMinimumReceive:   sdkconfig.Duration(300 * time.Millisecond),
				DetectionMultiplier:      3,
			})
		}, NodeTimeout(5*time.Minute))

		It("brings the bfd session with the router up", func(ctx SpecContext) {
			Eventually(bfdSessionState(ctx), "1m").Should(Equal(apipb.BfdSessionState_BFD_SESSION_STATE_UP))
		})

		It("announces a vip on the session watched by bfd", func(ctx SpecContext) {
			e2eEnv.Controller.Push(instance, res.Vip("vip-bfd", vipIP, starling_announce.StarlingAnnounce_BGP))
			Eventually(func() ([]env.BgpRoute, error) { return peer.Rib(ctx) }, "1m").Should(ContainElement(env.BgpRoute{
				Prefix:      vipIP + "/32",
				NextHop:     agentIP,
				Communities: []string{"65000:42"},
			}))
		})

		It("withdraws the routes of a hung agent well before the hold time", func(ctx SpecContext) {
			Eventually(func() ([]env.BgpRoute, error) { return peer.Rib(ctx) }, "1m").ShouldNot(BeEmpty())

			Expect(agent.Freeze(ctx)).To(Succeed())
			DeferCleanup(agent.Resume)

			// 3 missed packets of 300ms take the session down, the 90s hold time is never reached
			Eventually(func() ([]env.BgpRoute, error) { return peer.Rib(ctx) }, "15s", "200ms").Should(BeEmpty())
			Expect(bfdSessionState(ctx)()).ToNot(Equal(apipb.BfdSessionState_BFD_SESSION_STATE_UP))
		}, NodeTimeout(2*time.Minute))

		It("announces again once the agent is back", func(ctx SpecContext) {
			Expect(agent.Resume(ctx)).To(Succeed())

			Eventually(bfdSessionState(ctx), "1m").Should(Equal(apipb.BfdSessionState_BFD_SESSION_STATE_UP))
			Eventually(func() ([]env.BgpRoute, error) { return peer.Rib(ctx) }, "1m").Should(ContainElement(
				HaveField("Prefix", vipIP+"/32"),
			))
		}, NodeTimeout(3*time.Minute))
	})
})
