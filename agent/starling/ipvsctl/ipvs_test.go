package ipvsctl_test

import (
	"fmt"
	"net"

	"github.com/cloudflare/ipvs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/agent/starling/testhelpers"
	"go.uber.org/mock/gomock"

	. "github.com/seagalia/flock/agent/starling/ipvsctl"
)

var _ = Describe("Ipvsctl", func() {
	var (
		mockCtrl   *gomock.Controller
		mockClient *testhelpers.MockClient
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockClient = testhelpers.NewMockClient(mockCtrl)
		SetClient(mockClient)
	})

	AfterEach(func() {
		SetClient(NewFakeIpvsClient())
	})

	Context("RemoveServicesOn", func() {
		It("should remove only the services on the given addresses", func() {
			onVip := makeService("10.0.0.1/32", 80, ipvs.TCP)
			onVipUdp := makeService("10.0.0.1/32", 53, ipvs.UDP)
			notOnVip := makeService("10.0.0.2/32", 80, ipvs.TCP)
			mockClient.EXPECT().Services().Return([]ipvs.ServiceExtended{
				{Service: onVip}, {Service: notOnVip}, {Service: onVipUdp},
			}, nil)
			mockClient.EXPECT().RemoveService(onVip).Return(nil)
			mockClient.EXPECT().RemoveService(onVipUdp).Return(nil)

			Expect(RemoveServicesOn([]net.IP{net.ParseIP("10.0.0.1")})).To(Succeed())
		})
		It("should not list services without addresses", func() {
			Expect(RemoveServicesOn(nil)).To(Succeed())
		})
		It("should keep removing after a failure and report it", func() {
			first := makeService("10.0.0.1/32", 80, ipvs.TCP)
			second := makeService("10.0.0.1/32", 443, ipvs.TCP)
			mockClient.EXPECT().Services().Return([]ipvs.ServiceExtended{{Service: first}, {Service: second}}, nil)
			mockClient.EXPECT().RemoveService(first).Return(fmt.Errorf("busy"))
			mockClient.EXPECT().RemoveService(second).Return(nil)

			Expect(RemoveServicesOn([]net.IP{net.ParseIP("10.0.0.1")})).To(MatchError(ContainSubstring("busy")))
		})
	})

	Context("ServiceToIpNet", func() {
		It("should convert an ipv4 service", func() {
			Expect(ServiceToIpNet(makeService("10.0.0.1/32", 80, ipvs.TCP)).String()).To(Equal("10.0.0.1/32"))
			Expect(ServiceToIpNet(makeService("10.0.0.0/24", 80, ipvs.TCP)).String()).To(Equal("10.0.0.0/24"))
		})
		It("should convert an ipv6 service", func() {
			Expect(ServiceToIpNet(makeService("2001:db8::1/128", 80, ipvs.TCP)).String()).To(Equal("2001:db8::1/128"))
		})
	})
})
