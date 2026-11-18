package handlers_test

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"go.uber.org/mock/gomock"

	"github.com/seagalia/flock/agent/starling/envoyctl"
	. "github.com/seagalia/flock/agent/starling/handlers"
	"github.com/seagalia/flock/agent/starling/layer2"
	"github.com/seagalia/flock/agent/starling/testhelpers"
)

var _ = Describe("BgpHandler", func() {
	var (
		mockCtrl *gomock.Controller
		mockReg  *testhelpers.MockBgpRegisterer
		handler  *BgpHandler
		ctx      = context.Background()
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockReg = testhelpers.NewMockBgpRegisterer(mockCtrl)
		handler = NewBgpHandler(mockReg)
	})

	When("vip is announced through native bgp", func() {
		It("should register and unregister vip", func() {
			aVip := makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_BGP)
			mockReg.EXPECT().AddVip(ctx, aVip).Return(nil)
			mockReg.EXPECT().DelVip(ctx, aVip).Return(nil)
			Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())
			Expect(handler.OnMessageRemove(ctx, aVip)).To(Succeed())
		})

		It("should return registerer errors", func() {
			aVip := makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_BGP)
			mockReg.EXPECT().AddVip(ctx, aVip).Return(fmt.Errorf("bgp down"))
			Expect(handler.OnMessageAdded(ctx, aVip)).To(MatchError("bgp down"))
		})
	})

	It("should never announce a wildcard vip", func() {
		// the mock fails on any AddVip or DelVip call
		aVip := makeVip("vip-any", "0.0.0.0", starling_announce.StarlingAnnounce_BGP)
		Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())
		Expect(handler.OnMessageRemove(ctx, aVip)).To(Succeed())
	})

	When("vip is announced another way", func() {
		It("should ignore vip", func() {
			aVip := makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2)
			Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())
			Expect(handler.OnMessageRemove(ctx, aVip)).To(Succeed())
		})
	})
})

var _ = Describe("Layer2Handler", func() {
	var (
		mockCtrl      *gomock.Controller
		mockAnnouncer *testhelpers.MockL2Announcer
		handler       *Layer2Handler
		ctx           = context.Background()
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockAnnouncer = testhelpers.NewMockL2Announcer(mockCtrl)
		handler = NewLayer2Handler("node-1", []string{"eth0"}, false, mockAnnouncer)
	})

	DescribeTable("should announce layer2 vip on configured interfaces",
		func(ip string, expectedName string) {
			aVip := makeVip("vip-1", ip, starling_announce.StarlingAnnounce_LAYER2)
			expectedAdv := layer2.NewIPAdvertisement(netip.MustParseAddr(ip), false, []string{"eth0"})
			mockAnnouncer.EXPECT().SetBalancer(expectedName, expectedAdv)
			mockAnnouncer.EXPECT().DeleteBalancer(expectedName)

			Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())
			Expect(handler.OnMessageRemove(ctx, aVip)).To(Succeed())
		},
		Entry("ipv4", "10.0.0.1", "10.0.0.1/32"),
		Entry("ipv6", "2001:db8::1", "2001:db8::1/128"),
	)

	DescribeTable("should never announce a wildcard vip",
		func(ip string) {
			// the mock fails on any SetBalancer or DeleteBalancer call
			aVip := makeVip("vip-any", ip, starling_announce.StarlingAnnounce_LAYER2)
			Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())
			Expect(handler.OnMessageRemove(ctx, aVip)).To(Succeed())
		},
		Entry("ipv4", "0.0.0.0"),
		Entry("ipv6", "::"),
	)

	It("should ignore vip not announced through layer2", func() {
		aVip := makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_BGP)
		Expect(handler.OnMessageAdded(ctx, aVip)).To(Succeed())
		Expect(handler.OnMessageRemove(ctx, aVip)).To(Succeed())
	})
})

var _ = Describe("BackendHandler", func() {
	It("should delegate to jointer", func() {
		mockCtrl := gomock.NewController(GinkgoT())
		mockJointer := testhelpers.NewMockJointer(mockCtrl)
		handler := NewBackendHandler(mockJointer)
		aBackend := makeBackend("backend-1")
		ctx := context.Background()

		mockJointer.EXPECT().OnMessageAdded(ctx, aBackend).Return(nil)
		mockJointer.EXPECT().OnMessageRemove(ctx, aBackend).Return(fmt.Errorf("remove failed"))

		Expect(handler.Name()).To(Equal("backend"))
		Expect(handler.OnMessageAdded(ctx, aBackend)).To(Succeed())
		Expect(handler.OnMessageRemove(ctx, aBackend)).To(MatchError("remove failed"))
		// mock jointer is not stoppable
		Expect(handler.(*BackendHandler).Stop()).To(Succeed())
	})
})

var _ = Describe("CertificateHandler", func() {
	It("should push certificate as envoy secret", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cw := envoyctl.NewConfigWatcher(ctx, false, rt.Store, rt.States)
		cw.RunRequestQueues()
		sds := startEnvoyStub(ctx, cw, helpers.TypeUrl[*tlsv3.Secret]())

		handler := NewCertificateHandler(cw)
		Expect(handler.Name()).To(Equal("certificate"))
		aCert := &cert.Certificate{UUID: "cert-1", CertChain: "chain", PrivateKey: "key"}

		Expect(handler.OnMessageAdded(ctx, aCert)).To(Succeed())
		Eventually(sds.ResourceNames, 5*time.Second).Should(Equal([]string{"cert-1"}))
		secret := &tlsv3.Secret{}
		sds.LastResource(secret)
		Expect(secret.GetTlsCertificate().GetCertificateChain().GetInlineString()).To(Equal("chain"))

		Expect(handler.OnMessageRemove(ctx, aCert)).To(Succeed())
		Eventually(sds.ResourceNames, 5*time.Second).Should(Equal([]string{"cert-1", "-cert-1"}))
	})

	It("should not push a certificate without chain or key", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cw := envoyctl.NewConfigWatcher(ctx, false, rt.Store, rt.States)
		cw.RunRequestQueues()
		sds := startEnvoyStub(ctx, cw, helpers.TypeUrl[*tlsv3.Secret]())

		handler := NewCertificateHandler(cw)
		Expect(handler.OnMessageAdded(ctx, &cert.Certificate{UUID: "ca-only", Ca: "ca"})).To(Succeed())
		Consistently(sds.ResourceNames, 300*time.Millisecond).Should(BeEmpty())
	})
})
