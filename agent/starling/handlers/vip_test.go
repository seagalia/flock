package handlers_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	agtesthelpers "github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"go.uber.org/mock/gomock"

	"github.com/seagalia/flock/agent/starling/agentlist"
	. "github.com/seagalia/flock/agent/starling/handlers"
	"github.com/seagalia/flock/agent/starling/testhelpers"
)

var _ = Describe("VipHandler", func() {
	var (
		mockCtrl  *gomock.Controller
		mockNm    *testhelpers.MockNetworkManager
		mockAd    *testhelpers.MockAttachDetachHandler
		handler   adshandlers.MessageHandler[*vip.VIP]
		ctx       = context.Background()
		aVip      *vip.VIP
		vipIpNet  = ipNet("10.0.0.1/32")
		errFailed = fmt.Errorf("failed")
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockNm = testhelpers.NewMockNetworkManager(mockCtrl)
		mockAd = testhelpers.NewMockAttachDetachHandler(mockCtrl)
		handler = NewVipHandler(mockNm, mockAd, rt.Store.VIPs())
		aVip = makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2, "vs-1", "vs-2")
	})

	Context("OnMessageAdded", func() {
		It("should add local route and attach every virtual servers", func() {
			mockNm.EXPECT().AddVip(vipIpNet).Return(nil)
			mockAd.EXPECT().AttachVip(ctx, aVip, "vs-1").Return(nil)
			mockAd.EXPECT().AttachVip(ctx, aVip, "vs-2").Return(nil)
			Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())
		})

		It("should fail when local route can't be added", func() {
			mockNm.EXPECT().AddVip(vipIpNet).Return(errFailed)
			Expect(handler.OnMessageAdded(ctx, aVip)).To(MatchError(errFailed))
		})

		It("should attach and detach only the difference on update", func() {
			mockNm.EXPECT().AddVip(vipIpNet).Return(nil).Times(2)
			mockAd.EXPECT().AttachVip(ctx, gomock.Any(), gomock.Any()).Return(nil).Times(2)
			Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())

			updatedVip := makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2, "vs-2", "vs-3")
			mockAd.EXPECT().DetachVip(ctx, updatedVip, "vs-1").Return(nil)
			mockAd.EXPECT().AttachVip(ctx, updatedVip, "vs-3").Return(nil)
			Expect(handler.OnMessageAdded(ctx, updatedVip)).To(Succeed())
		})

		It("should retry attaching on next add when attach failed", func() {
			mockNm.EXPECT().AddVip(vipIpNet).Return(nil).Times(2)
			mockAd.EXPECT().AttachVip(ctx, aVip, "vs-1").Return(errFailed)
			mockAd.EXPECT().AttachVip(ctx, aVip, "vs-2").Return(nil)
			Expect(handler.OnMessageAdded(ctx, aVip)).To(MatchError(ContainSubstring("failed")))

			// vip was not cached so everything is attached again
			mockAd.EXPECT().AttachVip(ctx, aVip, "vs-1").Return(nil)
			mockAd.EXPECT().AttachVip(ctx, aVip, "vs-2").Return(nil)
			Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())
		})

		It("should report detach and attach errors on update", func() {
			mockNm.EXPECT().AddVip(vipIpNet).Return(nil).Times(2)
			mockAd.EXPECT().AttachVip(ctx, gomock.Any(), gomock.Any()).Return(nil).Times(2)
			Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())

			updatedVip := makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2, "vs-3")
			mockAd.EXPECT().DetachVip(ctx, updatedVip, gomock.Any()).Return(errFailed).Times(2)
			mockAd.EXPECT().AttachVip(ctx, updatedVip, "vs-3").Return(errFailed)
			err := handler.OnMessageAdded(ctx, updatedVip)
			Expect(err).To(MatchError(ContainSubstring("failed to detach vip vip-1 to virtual server vs-1")))
			Expect(err).To(MatchError(ContainSubstring("failed to atach vip vip-1 to virtual server vs-3")))
		})
	})

	Context("OnMessageRemove", func() {
		It("should remove local route and detach every virtual servers", func() {
			mockNm.EXPECT().DelVip(vipIpNet).Return(nil)
			mockAd.EXPECT().DetachVip(ctx, aVip, "vs-1").Return(nil)
			mockAd.EXPECT().DetachVip(ctx, aVip, "vs-2").Return(nil)
			Expect(handler.OnMessageRemove(ctx, aVip)).To(Succeed())
		})

		It("should fail when local route can't be removed", func() {
			mockNm.EXPECT().DelVip(vipIpNet).Return(errFailed)
			Expect(handler.OnMessageRemove(ctx, aVip)).To(MatchError(errFailed))
		})

		It("should fail when detach fails", func() {
			mockNm.EXPECT().DelVip(vipIpNet).Return(nil)
			mockAd.EXPECT().DetachVip(ctx, aVip, "vs-1").Return(errFailed)
			Expect(handler.OnMessageRemove(ctx, aVip)).To(MatchError(errFailed))
		})
	})

	Context("with a wildcard vip", func() {
		DescribeTable("should never put it on the dummy interface",
			func(ip string) {
				// the mock fails on any AddVip or DelVip call
				wildcard := makeVip("vip-any", ip, starling_announce.StarlingAnnounce_LAYER2, "vs-1")
				mockAd.EXPECT().AttachVip(ctx, wildcard, "vs-1").Return(nil)
				Expect(handler.OnMessageAdded(ctx, wildcard)).To(Succeed())

				mockAd.EXPECT().DetachVip(ctx, wildcard, "vs-1").Return(nil)
				Expect(handler.OnMessageRemove(ctx, wildcard)).To(Succeed())
			},
			Entry("ipv4", "0.0.0.0"),
			Entry("ipv6", "::"),
		)

		It("should leave it out of the clean", func() {
			Expect(rt.Store.VIPs().Create(context.Background(), makeVip("vip-any", "0.0.0.0", starling_announce.StarlingAnnounce_STATIC))).To(Succeed())
			Expect(rt.Store.VIPs().Create(context.Background(), makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			mockNm.EXPECT().DelVip(ipNet("10.0.0.1/32")).Return(nil)
			Expect(handler.(adshandlers.Cleanable).Clean()).To(Succeed())
		})
	})

	Context("Clean", func() {
		It("should remove local routes of every stored vips", func() {
			Expect(rt.Store.VIPs().Create(context.Background(), makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			Expect(rt.Store.VIPs().Create(context.Background(), makeVip("vip-2", "10.0.0.2", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			mockNm.EXPECT().DelVip(ipNet("10.0.0.1/32")).Return(nil)
			mockNm.EXPECT().DelVip(ipNet("10.0.0.2/32")).Return(errFailed)

			err := handler.(adshandlers.Cleanable).Clean()
			Expect(err).To(MatchError(ContainSubstring("failed")))
		})
	})
})

var _ = Describe("DynamicPlacementVipHandler", func() {
	var (
		mockCtrl  *gomock.Controller
		innerMock *agtesthelpers.MockMessageHandler[*vip.VIP]
		placer    *agentlist.VipPlacer
		handler   *DynamicPlacementVipHandler
		ctx       = context.Background()
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		innerMock = agtesthelpers.NewMockMessageHandler[*vip.VIP](mockCtrl)
		placer = agentlist.NewVipPlacer(context.Background(), "node-1", nil, nil, rt.Store.VIPs())
		placer.AddPlaceOn(starling_announce.StarlingAnnounce_LAYER2)
		handler = NewDynamicPlacementVipHandler(placer, innerMock, rt.Store.VIPs())
	})

	AfterEach(func() {
		Expect(handler.Stop()).To(Succeed())
	})

	It("should be named from inner handler", func() {
		innerMock.EXPECT().Name().Return("vip")
		Expect(handler.Name()).To(Equal("dynamic_placement_for_vip"))
	})

	It("should apply vip on inner handler when placed on this node", func() {
		aVip := makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2)
		Expect(rt.Store.VIPs().Create(context.Background(), aVip)).To(Succeed())
		innerMock.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, v *vip.VIP) error {
			defer GinkgoRecover()
			Expect(v.GetUUID()).To(Equal("vip-1"))
			return nil
		})
		Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())
		Expect(placer.AnnouncedVips()).To(HaveLen(1))
	})

	It("should not apply vip of an announce type not placed", func() {
		aVip := makeVip("vip-bgp", "10.0.0.1", starling_announce.StarlingAnnounce_BGP)
		Expect(rt.Store.VIPs().Create(context.Background(), aVip)).To(Succeed())
		Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())
		Expect(placer.AnnouncedVips()).To(BeEmpty())
	})

	It("should remove vip from store and inner handler", func() {
		aVip := makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2)
		Expect(rt.Store.VIPs().Create(context.Background(), aVip)).To(Succeed())
		innerMock.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil)
		Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())

		innerMock.EXPECT().OnMessageRemove(gomock.Any(), gomock.Any()).Return(nil)
		Expect(handler.OnMessageRemove(ctx, aVip)).To(Succeed())
		exists, err := rt.Store.VIPs().Exists(context.Background(), "vip-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeFalse())
		Expect(placer.AnnouncedVips()).To(BeEmpty())
	})

	It("should rollback vip in store when inner handler fails to remove", func() {
		aVip := makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2)
		Expect(rt.Store.VIPs().Create(context.Background(), aVip)).To(Succeed())
		innerMock.EXPECT().OnMessageAdded(gomock.Any(), gomock.Any()).Return(nil)
		Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())

		innerMock.EXPECT().OnMessageRemove(gomock.Any(), gomock.Any()).Return(fmt.Errorf("failed"))
		Expect(handler.OnMessageRemove(ctx, aVip)).To(MatchError(ContainSubstring("unable to recalculate and apply")))
		exists, err := rt.Store.VIPs().Exists(context.Background(), "vip-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(exists).To(BeTrue())
	})

	It("should consider removing a vip unknown in store as a no-op", func() {
		Expect(handler.OnMessageRemove(ctx, makeVip("unknown", "10.0.0.9", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
	})
})
