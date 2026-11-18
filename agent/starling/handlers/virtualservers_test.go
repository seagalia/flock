package handlers_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/limit/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/anypb"

	. "github.com/seagalia/flock/agent/starling/handlers"
	"github.com/seagalia/flock/agent/starling/testhelpers"
)

type cleanableRouteHandler[T vs.IsRouteConfig] struct {
	*testhelpers.MockRouteHandler[T]
	cleanErr error
	cleaned  bool
}

func (c *cleanableRouteHandler[T]) Clean() error {
	c.cleaned = true
	return c.cleanErr
}

var _ = Describe("VsHandler", func() {
	var (
		mockCtrl  *gomock.Controller
		mockNm    *testhelpers.MockNetworkManager
		tcpRte    *testhelpers.MockRouteHandler[*vs.TcpRouteConfig]
		udpRte    *testhelpers.MockRouteHandler[*vs.UdpRouteConfig]
		httpRte   *testhelpers.MockRouteHandler[*vs.HttpRoutesConfig]
		tlsTcpRte *testhelpers.MockRouteHandler[*vs.TlsTcpRoutesConfig]
		handler   *VsHandler
		ctx       = context.Background()
		vip1      *vip.VIP
		vipIpNet  = ipNet("10.0.0.1/32")
		errFailed = fmt.Errorf("failed")
	)

	tcpVirtServ := func(backendUuid string, port uint32) *vs.VirtualServer {
		return &vs.VirtualServer{UUID: "vs-1", Port: port, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
			TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: backendUuid},
		}}
	}

	httpVirtServ := func() *vs.VirtualServer {
		return &vs.VirtualServer{UUID: "vs-1", Port: 80, EnableL3Log: true, RouteConfig: &vs.VirtualServer_HttpRoutesConfig{
			HttpRoutesConfig: &vs.HttpRoutesConfig{VirtualHosts: []*route.VirtualHost{{Domains: []string{"*"}}}},
		}}
	}

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockNm = testhelpers.NewMockNetworkManager(mockCtrl)
		tcpRte = testhelpers.NewMockRouteHandler[*vs.TcpRouteConfig](mockCtrl)
		udpRte = testhelpers.NewMockRouteHandler[*vs.UdpRouteConfig](mockCtrl)
		httpRte = testhelpers.NewMockRouteHandler[*vs.HttpRoutesConfig](mockCtrl)
		tlsTcpRte = testhelpers.NewMockRouteHandler[*vs.TlsTcpRoutesConfig](mockCtrl)
		handler = NewVsHandler(mockNm, tcpRte, udpRte, httpRte, tlsTcpRte, rt.Store)

		// l3 logs are always cleared when l3 log is disabled or a route is removed
		mockNm.EXPECT().DelNfLog(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		mockNm.EXPECT().DelMarkLog(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		vip1 = makeVip("vip1-aaaa", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2, "vs-1")
		Expect(rt.Store.VIPs().Create(context.Background(), vip1)).To(Succeed())
	})

	Context("OnMessageAdded", func() {
		It("should create route for every vip of the virtual server", func() {
			vip2 := makeVip("vip2-bbbb", "10.0.0.2", starling_announce.StarlingAnnounce_LAYER2, "vs-1")
			Expect(rt.Store.VIPs().Create(context.Background(), vip2)).To(Succeed())
			virtServ := tcpVirtServ("backend-1", 80)

			tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), virtServ, virtServ.GetTcpRouteConfig()).Return(nil).Times(2)
			Expect(handler.OnMessageAdded(ctx, virtServ)).To(Succeed())
		})

		It("should do nothing when virtual server did not change", func() {
			tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, tcpVirtServ("backend-1", 80))).To(Succeed())
			Expect(handler.OnMessageAdded(ctx, tcpVirtServ("backend-1", 80))).To(Succeed())
		})

		It("should update route when virtual server changed", func() {
			tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, tcpVirtServ("backend-1", 80))).To(Succeed())

			updated := tcpVirtServ("backend-2", 80)
			tcpRte.EXPECT().UpdateRoute(ctx, gomock.Any(), updated, updated.GetTcpRouteConfig()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, updated)).To(Succeed())
		})

		It("should recreate route when port changed", func() {
			first := tcpVirtServ("backend-1", 80)
			tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), first, gomock.Any()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, first)).To(Succeed())

			updated := tcpVirtServ("backend-1", 8080)
			gomock.InOrder(
				tcpRte.EXPECT().DeleteRoute(gomock.Any(), gomock.Any(), first, gomock.Any()).Return(nil),
				tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), updated, gomock.Any()).Return(nil),
			)
			Expect(handler.OnMessageAdded(ctx, updated)).To(Succeed())
		})

		It("should move route to the right handler when route type changed", func() {
			tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, tcpVirtServ("backend-1", 53))).To(Succeed())

			udpVirtServ := &vs.VirtualServer{UUID: "vs-1", Port: 53, RouteConfig: &vs.VirtualServer_UdpRouteConfig{
				UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: "backend-1"},
			}}
			tcpRte.EXPECT().DeleteRoute(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			udpRte.EXPECT().CreateRoute(ctx, gomock.Any(), udpVirtServ, udpVirtServ.GetUdpRouteConfig()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, udpVirtServ)).To(Succeed())
		})

		It("should retry route creation on next add when it failed", func() {
			tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(errFailed)
			Expect(handler.OnMessageAdded(ctx, tcpVirtServ("backend-1", 80))).To(MatchError(ContainSubstring("failed")))

			tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, tcpVirtServ("backend-1", 80))).To(Succeed())
		})

		It("should set l3 log and mark log for http virtual server", func() {
			virtServ := httpVirtServ()
			mockNm.EXPECT().AddNfLog(vipIpNet, uint16(80), "vs-1", false).Return(nil)
			httpRte.EXPECT().CreateRoute(ctx, gomock.Any(), virtServ, virtServ.GetHttpRoutesConfig()).Return(nil)
			mockNm.EXPECT().AddMarkLog(vipIpNet, uint16(80), false).Return(nil)
			Expect(handler.OnMessageAdded(ctx, virtServ)).To(Succeed())
		})

		It("should report route error even when l3 log is enabled", func() {
			virtServ := httpVirtServ()
			mockNm.EXPECT().AddNfLog(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			httpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(errFailed)
			Expect(handler.OnMessageAdded(ctx, virtServ)).To(MatchError(ContainSubstring("failed to create or update route")))
		})

		It("should fail when l3 log can't be set", func() {
			virtServ := httpVirtServ()
			mockNm.EXPECT().AddNfLog(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errFailed)
			Expect(handler.OnMessageAdded(ctx, virtServ)).To(MatchError(ContainSubstring("failed to add nflog")))
		})

		Context("with limits", func() {
			withLimits := func(virtServ *vs.VirtualServer, maxConn uint64, rate uint64) *vs.VirtualServer { //nolint:unparam // the call sites show the value
				virtServ.LimitConfig = []*limit.Config{
					{Action: &limit.Config_ConnectionLimit{ConnectionLimit: &limit.ConnectionLimit{MaxConnections: maxConn, SourceAddress: true}}},
					{Action: &limit.Config_ConnectionRateLimit{ConnectionRateLimit: &limit.ConnectionRateLimit{Rate: rate, Burst: 5, Unit: flocktype.TimeUnit_SECOND}}},
				}
				return virtServ
			}

			It("should add connection limits with the route", func() {
				mockNm.EXPECT().AddConnLimit(vipIpNet, uint16(80), false, uint64(100), true, false).Return(nil)
				mockNm.EXPECT().AddConnRateLimit(vipIpNet, uint16(80), false, "10/second", uint64(5), false, false).Return(nil)
				tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				Expect(handler.OnMessageAdded(ctx, withLimits(tcpVirtServ("backend-1", 80), 100, 10))).To(Succeed())
			})

			It("should replace changed limits", func() {
				mockNm.EXPECT().AddConnLimit(gomock.Any(), gomock.Any(), gomock.Any(), uint64(100), gomock.Any(), gomock.Any()).Return(nil)
				mockNm.EXPECT().AddConnRateLimit(gomock.Any(), gomock.Any(), gomock.Any(), "10/second", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				Expect(handler.OnMessageAdded(ctx, withLimits(tcpVirtServ("backend-1", 80), 100, 10))).To(Succeed())

				mockNm.EXPECT().DelConnLimit(vipIpNet, uint16(80), false, uint64(100), true, false).Return(nil)
				mockNm.EXPECT().AddConnLimit(gomock.Any(), gomock.Any(), gomock.Any(), uint64(200), gomock.Any(), gomock.Any()).Return(nil)
				// rate limit did not change but is reapplied (nm calls are idempotent)
				mockNm.EXPECT().AddConnRateLimit(gomock.Any(), gomock.Any(), gomock.Any(), "10/second", gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				tcpRte.EXPECT().UpdateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				Expect(handler.OnMessageAdded(ctx, withLimits(tcpVirtServ("backend-1", 80), 200, 10))).To(Succeed())
			})

			It("should remove every limits when none are set anymore", func() {
				mockNm.EXPECT().AddConnLimit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				mockNm.EXPECT().AddConnRateLimit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				Expect(handler.OnMessageAdded(ctx, withLimits(tcpVirtServ("backend-1", 80), 100, 10))).To(Succeed())

				mockNm.EXPECT().DelConnLimit(vipIpNet, uint16(80), false, uint64(100), true, false).Return(nil)
				mockNm.EXPECT().DelConnRateLimit(vipIpNet, uint16(80), false, "10/second", uint64(5), false, false).Return(nil)
				tcpRte.EXPECT().UpdateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				Expect(handler.OnMessageAdded(ctx, tcpVirtServ("backend-1", 80))).To(Succeed())
			})
		})
	})

	Context("OnMessageRemove", func() {
		It("should delete routes of the virtual server", func() {
			virtServ := tcpVirtServ("backend-1", 80)
			tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, virtServ)).To(Succeed())

			tcpRte.EXPECT().DeleteRoute(gomock.Any(), gomock.Any(), virtServ, virtServ.GetTcpRouteConfig()).Return(nil)
			Expect(handler.OnMessageRemove(ctx, virtServ)).To(Succeed())

			// route is not cached anymore, adding again creates it
			tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, virtServ)).To(Succeed())
		})

		It("should not delete routes of other virtual servers", func() {
			otherVip := makeVip("vip9-cccc", "10.0.0.9", starling_announce.StarlingAnnounce_LAYER2, "vs-10")
			Expect(rt.Store.VIPs().Create(context.Background(), otherVip)).To(Succeed())
			otherVs := &vs.VirtualServer{UUID: "vs-10", Port: 80, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
				TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: "backend-1"},
			}}
			tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)
			Expect(handler.OnMessageAdded(ctx, tcpVirtServ("backend-1", 80))).To(Succeed())
			Expect(handler.OnMessageAdded(ctx, otherVs)).To(Succeed())

			// "vs-1" is a prefix of "vs-10" but they must not be confused
			tcpRte.EXPECT().DeleteRoute(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, _ *vip.VIP, virtServ *vs.VirtualServer, _ *vs.TcpRouteConfig) error {
					defer GinkgoRecover()
					Expect(virtServ.GetUUID()).To(Equal("vs-1"))
					return nil
				})
			Expect(handler.OnMessageRemove(ctx, tcpVirtServ("backend-1", 80))).To(Succeed())
		})

		Context("on a port shared by virtual servers", func() {
			var (
				sharedVip *vip.VIP
				first     *vs.VirtualServer
				second    *vs.VirtualServer
			)
			sharedIpNet := ipNet("10.0.0.3/32")

			limitedTlsVs := func(uuid, serverName string) *vs.VirtualServer {
				return &vs.VirtualServer{
					UUID: uuid,
					Port: 443,
					LimitConfig: []*limit.Config{
						{Action: &limit.Config_ConnectionLimit{ConnectionLimit: &limit.ConnectionLimit{MaxConnections: 100, SourceAddress: true}}},
						{Action: &limit.Config_ConnectionRateLimit{ConnectionRateLimit: &limit.ConnectionRateLimit{Rate: 10, Burst: 5, Unit: flocktype.TimeUnit_SECOND}}},
					},
					RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: []*vs.TlsTcpRouteConfig{{
						BackendUuid: "backend-1",
						Mode:        &vs.TlsTcpRouteConfig_Passthrough{Passthrough: &vs.TlsPassthrough{ServerNames: []string{serverName}}},
					}}}},
				}
			}

			BeforeEach(func() {
				sharedVip = makeVip("vip3-cccc", "10.0.0.3", starling_announce.StarlingAnnounce_LAYER2, "vs-a", "vs-b")
				Expect(rt.Store.VIPs().Create(context.Background(), sharedVip)).To(Succeed())
				first = limitedTlsVs("vs-a", "a.local")
				second = limitedTlsVs("vs-b", "b.local")
				mockNm.EXPECT().AddConnLimit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				mockNm.EXPECT().AddConnRateLimit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				tlsTcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)
				tlsTcpRte.EXPECT().DeleteRoute(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				Expect(handler.OnMessageAdded(ctx, first)).To(Succeed())
				Expect(handler.OnMessageAdded(ctx, second)).To(Succeed())
			})

			It("should keep the limits of the port while another virtual server uses it", func() {
				mockNm.EXPECT().DelConnLimit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				mockNm.EXPECT().DelConnRateLimit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				Expect(handler.OnMessageRemove(ctx, first)).To(Succeed())
			})

			It("should remove the limits of the port with its last virtual server", func() {
				Expect(handler.OnMessageRemove(ctx, first)).To(Succeed())

				mockNm.EXPECT().DelConnLimit(sharedIpNet, uint16(443), false, uint64(100), true, false).Return(nil)
				mockNm.EXPECT().DelConnRateLimit(sharedIpNet, uint16(443), false, "10/second", uint64(5), false, false).Return(nil)
				Expect(handler.OnMessageRemove(ctx, second)).To(Succeed())
			})

			It("should keep the limits when the vip detaches one of the virtual servers of the port", func() {
				mockNm.EXPECT().DelConnLimit(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
				Expect(handler.DetachVip(ctx, sharedVip, "vs-a")).To(Succeed())
			})
		})

		Context("starling options on a port shared by virtual servers", func() {
			tlsVs := func(uuid string, options *starling_vsopts.TlsTcpOptions) *vs.VirtualServer {
				typedOptions, err := anypb.New(options)
				Expect(err).ToNot(HaveOccurred())
				return &vs.VirtualServer{
					UUID: uuid,
					Port: 443,
					RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{
						TypedOptions: typedOptions,
						Routes: []*vs.TlsTcpRouteConfig{{
							BackendUuid: "backend-1",
							Mode:        &vs.TlsTcpRouteConfig_Passthrough{Passthrough: &vs.TlsPassthrough{ServerNames: []string{uuid + ".local"}}},
						}},
					}},
				}
			}

			BeforeEach(func() {
				Expect(rt.Store.VIPs().Create(context.Background(), makeVip("vip3-cccc", "10.0.0.3", starling_announce.StarlingAnnounce_LAYER2, "vs-a", "vs-b"))).To(Succeed())
				tlsTcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(1)
				Expect(handler.OnMessageAdded(ctx, tlsVs("vs-a", &starling_vsopts.TlsTcpOptions{}))).To(Succeed())
			})

			It("should refuse a virtual server whose nat differs from another one of its port", func() {
				err := handler.OnMessageAdded(ctx, tlsVs("vs-b", &starling_vsopts.TlsTcpOptions{DisableSnat: true}))

				Expect(err).To(MatchError(ContainSubstring("disable_snat and disable_dnat must be the same as on virtual server vs-a")))
			})

			It("should accept a virtual server with the nat of the others of its port", func() {
				tlsTcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

				Expect(handler.OnMessageAdded(ctx, tlsVs("vs-b", &starling_vsopts.TlsTcpOptions{}))).To(Succeed())
			})
		})

		Context("l3 log on a port shared by virtual servers", func() {
			var strictNm *testhelpers.MockNetworkManager
			sharedIpNet := ipNet("10.0.0.3/32")

			tlsVs := func(uuid string, l3Log bool) *vs.VirtualServer {
				return &vs.VirtualServer{
					UUID:        uuid,
					Port:        443,
					EnableL3Log: l3Log,
					RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: []*vs.TlsTcpRouteConfig{{
						BackendUuid: "backend-1",
						Mode:        &vs.TlsTcpRouteConfig_Passthrough{Passthrough: &vs.TlsPassthrough{ServerNames: []string{uuid + ".local"}}},
					}}}},
				}
			}

			BeforeEach(func() {
				// the network manager of the suite accepts any deletion of the logs, these specs check them
				strictNm = testhelpers.NewMockNetworkManager(mockCtrl)
				handler = NewVsHandler(strictNm, tcpRte, udpRte, httpRte, tlsTcpRte, rt.Store)
				Expect(rt.Store.VIPs().Create(context.Background(), makeVip("vip3-cccc", "10.0.0.3", starling_announce.StarlingAnnounce_LAYER2, "vs-a", "vs-b"))).To(Succeed())
				tlsTcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
				tlsTcpRte.EXPECT().DeleteRoute(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

				strictNm.EXPECT().AddNfLog(sharedIpNet, uint16(443), "vs-a", false).Return(nil)
				strictNm.EXPECT().AddMarkLog(sharedIpNet, uint16(443), false).Return(nil)
				Expect(handler.OnMessageAdded(ctx, tlsVs("vs-a", true))).To(Succeed())
			})

			It("should keep the l3 log of the port when a virtual server without it joins", func() {
				strictNm.EXPECT().AddNfLog(sharedIpNet, uint16(443), "vs-a", false).Return(nil)
				Expect(handler.OnMessageAdded(ctx, tlsVs("vs-b", false))).To(Succeed())
			})

			It("should remove the l3 log of the port when the last virtual server enabling it leaves", func() {
				strictNm.EXPECT().AddNfLog(sharedIpNet, uint16(443), "vs-a", false).Return(nil)
				Expect(handler.OnMessageAdded(ctx, tlsVs("vs-b", false))).To(Succeed())

				strictNm.EXPECT().DelNfLog(sharedIpNet, uint16(443), gomock.Any(), false).Return(nil)
				strictNm.EXPECT().DelMarkLog(sharedIpNet, uint16(443), false).Return(nil)
				Expect(handler.OnMessageRemove(ctx, tlsVs("vs-a", true))).To(Succeed())
			})

			It("should log the port under a virtual server still on it when the one it was logged under leaves", func() {
				strictNm.EXPECT().AddNfLog(sharedIpNet, uint16(443), "vs-b", false).Return(nil)
				strictNm.EXPECT().AddMarkLog(sharedIpNet, uint16(443), false).Return(nil)
				Expect(handler.OnMessageAdded(ctx, tlsVs("vs-b", true))).To(Succeed())

				strictNm.EXPECT().AddNfLog(sharedIpNet, uint16(443), "vs-b", false).Return(nil)
				Expect(handler.OnMessageRemove(ctx, tlsVs("vs-a", true))).To(Succeed())
			})
		})

		It("should report delete errors", func() {
			tcpRte.EXPECT().CreateRoute(ctx, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, tcpVirtServ("backend-1", 80))).To(Succeed())

			tcpRte.EXPECT().DeleteRoute(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errFailed)
			Expect(handler.OnMessageRemove(ctx, tcpVirtServ("backend-1", 80))).To(MatchError(ContainSubstring("failed to remove virtual server vs-1")))
		})
	})

	Context("AttachVip / DetachVip", func() {
		var newVip *vip.VIP

		BeforeEach(func() {
			newVip = makeVip("vip2-bbbb", "10.0.0.2", starling_announce.StarlingAnnounce_LAYER2, "vs-1")
		})

		It("should ignore attach until virtual server is loaded", func() {
			Expect(handler.AttachVip(ctx, newVip, "vs-1")).To(Succeed())
		})

		It("should create route for attached vip", func() {
			virtServ := tcpVirtServ("backend-1", 80)
			tcpRte.EXPECT().CreateRoute(ctx, vip1, gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, virtServ)).To(Succeed())

			tcpRte.EXPECT().CreateRoute(ctx, newVip, virtServ, gomock.Any()).Return(nil)
			Expect(handler.AttachVip(ctx, newVip, "vs-1")).To(Succeed())
			// attaching twice is a no-op
			Expect(handler.AttachVip(ctx, newVip, "vs-1")).To(Succeed())
		})

		It("should delete route for detached vip", func() {
			tcpRte.EXPECT().CreateRoute(ctx, vip1, gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, tcpVirtServ("backend-1", 80))).To(Succeed())

			tcpRte.EXPECT().DeleteRoute(gomock.Any(), vip1, gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.DetachVip(ctx, vip1, "vs-1")).To(Succeed())
			// detaching an unknown vip is a no-op
			Expect(handler.DetachVip(ctx, vip1, "vs-1")).To(Succeed())
		})

		It("should attach a vip to a loaded virtual server which has no more vip", func() {
			virtServ := tcpVirtServ("backend-1", 80)
			Expect(rt.Store.VirtualServers().Create(context.Background(), virtServ)).To(Succeed())
			tcpRte.EXPECT().CreateRoute(ctx, vip1, gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.OnMessageAdded(ctx, virtServ)).To(Succeed())
			tcpRte.EXPECT().DeleteRoute(gomock.Any(), vip1, gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.DetachVip(ctx, vip1, "vs-1")).To(Succeed())

			tcpRte.EXPECT().CreateRoute(ctx, newVip, gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.AttachVip(ctx, newVip, "vs-1")).To(Succeed())
		})
	})

	Context("Clean", func() {
		It("should clean every cleanable route handlers and aggregate errors", func() {
			cleanableTcp := &cleanableRouteHandler[*vs.TcpRouteConfig]{MockRouteHandler: tcpRte}
			cleanableHttp := &cleanableRouteHandler[*vs.HttpRoutesConfig]{MockRouteHandler: httpRte, cleanErr: errFailed}
			handler = NewVsHandler(mockNm, cleanableTcp, udpRte, cleanableHttp, tlsTcpRte, rt.Store)

			Expect(handler.Clean()).To(MatchError(ContainSubstring("failed")))
			Expect(cleanableTcp.cleaned).To(BeTrue())
			Expect(cleanableHttp.cleaned).To(BeTrue())
		})
	})
})
