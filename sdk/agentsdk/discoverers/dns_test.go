package discoverers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"go.uber.org/mock/gomock"

	. "github.com/seagalia/flock/sdk/agentsdk/discoverers"
)

var _ = Describe("Dns", func() {
	var (
		mockCtrl       *gomock.Controller
		mockDnsHandler *testhelpers.MockDnsHandler
		dnsServer      *DNSServer
	)
	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockDnsHandler = testhelpers.NewMockDnsHandler(mockCtrl)
		dnsServer = NewDNSServer(mockDnsHandler)
		ready := make(chan struct{})
		go func() {
			dnsServer.Run(ready)
		}()
		<-ready
	})
	AfterEach(func() {
		dnsServer.Stop()
		mockCtrl.Finish()
		ClearCacheDns()
	})
	Context("Run", func() {
		When("Ipv6 is preferred", func() {
			It("should return only ipv6 ip when found", func() {
				handlerFunc := NewEasyHandler("test.com", []*core.IPAddr{
					{
						Ip: "10.0.0.1",
					},
					{
						Ip: "10.0.0.2",
					},
					{
						Ip: "2001:db8:0:85a3::ac1f:8001",
					},
					{
						Ip: "2001:db8:0:85a3::ac1f:8002",
					},
				}, false)
				dnsServer.LockHandler()
				mockDnsHandler.EXPECT().ServeDNS(gomock.Any(), gomock.Any()).DoAndReturn(handlerFunc.ServeDNS).Times(1)
				dnsServer.UnlockHandler()

				sd, err := NewDns("test.com", uint32(80), backend.DNS_V6_PREFERRED, dnsServer.IpAddr())
				Expect(err).ToNot(HaveOccurred())

				found := runDiscovery(sd)

				ipAddrs := <-found
				Expect(ipAddrs).To(HaveLen(2))
				Expect(ipAddrs[0].Ip).To(Or(Equal("2001:db8:0:85a3::ac1f:8001"), Equal("2001:db8:0:85a3::ac1f:8002")))
				Expect(ipAddrs[0].Port).To(Equal(uint32(80)))
				Expect(ipAddrs[1].Ip).To(Or(Equal("2001:db8:0:85a3::ac1f:8001"), Equal("2001:db8:0:85a3::ac1f:8002")))
				Expect(ipAddrs[1].Port).To(Equal(uint32(80)))
			})
			It("should return fallback to ipv4 ip when no ipv6 found", func() {
				handlerFunc := NewEasyHandler("test.com", []*core.IPAddr{
					{
						Ip: "10.0.0.1",
					},
					{
						Ip: "10.0.0.2",
					},
				}, false)
				dnsServer.LockHandler()
				mockDnsHandler.EXPECT().ServeDNS(gomock.Any(), gomock.Any()).DoAndReturn(handlerFunc.ServeDNS).AnyTimes()
				dnsServer.UnlockHandler()

				sd, err := NewDns("test.com", uint32(80), backend.DNS_V6_PREFERRED, dnsServer.IpAddr())
				Expect(err).ToNot(HaveOccurred())

				found := runDiscovery(sd)
				ipAddrs := <-found
				Expect(ipAddrs).To(HaveLen(2))
				Expect(ipAddrs[0].Ip).To(Or(Equal("10.0.0.1"), Equal("10.0.0.2")))
				Expect(ipAddrs[0].Port).To(Equal(uint32(80)))
				Expect(ipAddrs[1].Ip).To(Or(Equal("10.0.0.1"), Equal("10.0.0.2")))
				Expect(ipAddrs[1].Port).To(Equal(uint32(80)))
			})
		})
		When("Ipv6 only", func() {
			It("should return only ipv6 ip when found", func() {
				handlerFunc := NewEasyHandler("test.com", []*core.IPAddr{
					{
						Ip: "10.0.0.1",
					},
					{
						Ip: "10.0.0.2",
					},
					{
						Ip: "2001:db8:0:85a3::ac1f:8001",
					},
					{
						Ip: "2001:db8:0:85a3::ac1f:8002",
					},
				}, false)
				dnsServer.LockHandler()
				mockDnsHandler.EXPECT().ServeDNS(gomock.Any(), gomock.Any()).DoAndReturn(handlerFunc.ServeDNS).AnyTimes()
				dnsServer.UnlockHandler()

				sd, err := NewDns("test.com", uint32(80), backend.DNS_V6_ONLY, dnsServer.IpAddr())
				Expect(err).ToNot(HaveOccurred())

				found := runDiscovery(sd)

				ipAddrs := <-found
				Expect(ipAddrs).To(HaveLen(2))
				Expect(ipAddrs[0].Ip).To(Or(Equal("2001:db8:0:85a3::ac1f:8001"), Equal("2001:db8:0:85a3::ac1f:8002")))
				Expect(ipAddrs[0].Port).To(Equal(uint32(80)))
				Expect(ipAddrs[1].Ip).To(Or(Equal("2001:db8:0:85a3::ac1f:8001"), Equal("2001:db8:0:85a3::ac1f:8002")))
				Expect(ipAddrs[1].Port).To(Equal(uint32(80)))
			})
			It("should return nothing when no ipv6 found", func() {
				handlerFunc := NewEasyHandler("test.com", []*core.IPAddr{
					{
						Ip: "10.0.0.1",
					},
					{
						Ip: "10.0.0.2",
					},
				}, false)
				dnsServer.LockHandler()
				mockDnsHandler.EXPECT().ServeDNS(gomock.Any(), gomock.Any()).DoAndReturn(handlerFunc.ServeDNS).AnyTimes()
				dnsServer.UnlockHandler()

				sd, err := NewDns("test.com", uint32(80), backend.DNS_V6_ONLY, dnsServer.IpAddr())
				Expect(err).ToNot(HaveOccurred())

				found := runDiscovery(sd)

				ipAddrs := <-found
				Expect(ipAddrs).To(BeEmpty())
			})
		})
		When("Ipv4 is preferred", func() {
			It("should return only ipv4 ip when found", func() {
				handlerFunc := NewEasyHandler("test.com", []*core.IPAddr{
					{
						Ip: "10.0.0.1",
					},
					{
						Ip: "10.0.0.2",
					},
					{
						Ip: "2001:db8:0:85a3::ac1f:8001",
					},
					{
						Ip: "2001:db8:0:85a3::ac1f:8002",
					},
				}, false)
				dnsServer.LockHandler()
				mockDnsHandler.EXPECT().ServeDNS(gomock.Any(), gomock.Any()).DoAndReturn(handlerFunc.ServeDNS).AnyTimes()
				dnsServer.UnlockHandler()

				sd, err := NewDns("test.com", uint32(80), backend.DNS_V4_PREFERRED, dnsServer.IpAddr())
				Expect(err).ToNot(HaveOccurred())

				found := runDiscovery(sd)

				ipAddrs := <-found
				Expect(ipAddrs).To(HaveLen(2))
				Expect(ipAddrs[0].Ip).To(Or(Equal("10.0.0.1"), Equal("10.0.0.2")))
				Expect(ipAddrs[0].Port).To(Equal(uint32(80)))
				Expect(ipAddrs[1].Ip).To(Or(Equal("10.0.0.1"), Equal("10.0.0.2")))
				Expect(ipAddrs[1].Port).To(Equal(uint32(80)))
			})
			It("should return fallback to ipv6 ip when no ipv4 found", func() {
				handlerFunc := NewEasyHandler("test.com", []*core.IPAddr{
					{
						Ip: "2001:db8:0:85a3::ac1f:8001",
					},
					{
						Ip: "2001:db8:0:85a3::ac1f:8002",
					},
				}, false)
				dnsServer.LockHandler()
				mockDnsHandler.EXPECT().ServeDNS(gomock.Any(), gomock.Any()).DoAndReturn(handlerFunc.ServeDNS).AnyTimes()
				dnsServer.UnlockHandler()

				sd, err := NewDns("test.com", uint32(80), backend.DNS_V4_PREFERRED, dnsServer.IpAddr())
				Expect(err).ToNot(HaveOccurred())

				found := runDiscovery(sd)

				ipAddrs := <-found
				Expect(ipAddrs).To(HaveLen(2))
				Expect(ipAddrs[0].Ip).To(Or(
					Equal("2001:db8:0:85a3::ac1f:8001"),
					Equal("2001:db8:0:85a3::ac1f:8002"),
				))
				Expect(ipAddrs[0].Port).To(Equal(uint32(80)))
				Expect(ipAddrs[1].Ip).To(Or(
					Equal("2001:db8:0:85a3::ac1f:8001"),
					Equal("2001:db8:0:85a3::ac1f:8002"),
				))
				Expect(ipAddrs[1].Port).To(Equal(uint32(80)))
			})
		})
		When("Ipv4 only", func() {
			It("should return only ipv4 ip when found", func() {
				handlerFunc := NewEasyHandler("test.com", []*core.IPAddr{
					{
						Ip: "10.0.0.1",
					},
					{
						Ip: "10.0.0.2",
					},
					{
						Ip: "2001:db8:0:85a3::ac1f:8001",
					},
					{
						Ip: "2001:db8:0:85a3::ac1f:8002",
					},
				}, false)
				dnsServer.LockHandler()
				mockDnsHandler.EXPECT().ServeDNS(gomock.Any(), gomock.Any()).DoAndReturn(handlerFunc.ServeDNS).AnyTimes()
				dnsServer.UnlockHandler()

				sd, err := NewDns("test.com", uint32(80), backend.DNS_V4_ONLY, dnsServer.IpAddr())
				Expect(err).ToNot(HaveOccurred())

				found := runDiscovery(sd)

				ipAddrs := <-found
				Expect(ipAddrs).To(HaveLen(2))
				Expect(ipAddrs[0].Ip).To(Or(Equal("10.0.0.1"), Equal("10.0.0.2")))
				Expect(ipAddrs[0].Port).To(Equal(uint32(80)))
				Expect(ipAddrs[1].Ip).To(Or(Equal("10.0.0.1"), Equal("10.0.0.2")))
				Expect(ipAddrs[1].Port).To(Equal(uint32(80)))
			})
			It("should return nothing when no ipv4 found", func() {
				handlerFunc := NewEasyHandler("test.com", []*core.IPAddr{
					{
						Ip: "2001:db8:0:85a3::ac1f:8001",
					},
					{
						Ip: "2001:db8:0:85a3::ac1f:8002",
					},
				}, false)
				dnsServer.LockHandler()
				mockDnsHandler.EXPECT().ServeDNS(gomock.Any(), gomock.Any()).DoAndReturn(handlerFunc.ServeDNS).AnyTimes()
				dnsServer.UnlockHandler()

				sd, err := NewDns("test.com", uint32(80), backend.DNS_V4_ONLY, dnsServer.IpAddr())
				Expect(err).ToNot(HaveOccurred())

				found := runDiscovery(sd)

				ipAddrs := <-found
				Expect(ipAddrs).To(BeEmpty())
			})
		})
		When("First server can't find domain", func() {
			var (
				secondDnsHandler *testhelpers.MockDnsHandler
				secondDnsServer  *DNSServer
			)
			BeforeEach(func() {
				secondDnsHandler = testhelpers.NewMockDnsHandler(mockCtrl)
				secondDnsServer = NewDNSServer(secondDnsHandler)
				ready := make(chan struct{})
				go func() {
					secondDnsServer.Run(ready)
				}()
				<-ready
			})
			AfterEach(func() {
				secondDnsServer.Stop()
				ClearCacheDns()
			})
			It("should return from second server", func() {
				firstHandler := NewEasyHandler("toto.com", []*core.IPAddr{
					{
						Ip: "10.0.0.1",
					},
					{
						Ip: "10.0.0.2",
					},
				}, true)
				dnsServer.LockHandler()
				mockDnsHandler.EXPECT().ServeDNS(gomock.Any(), gomock.Any()).DoAndReturn(firstHandler.ServeDNS).MinTimes(1)
				dnsServer.UnlockHandler()
				handlerFunc := NewEasyHandler("test.com", []*core.IPAddr{
					{
						Ip: "10.0.0.1",
					},
					{
						Ip: "10.0.0.2",
					},
				}, true)
				secondDnsServer.LockHandler()
				secondDnsHandler.EXPECT().ServeDNS(gomock.Any(), gomock.Any()).
					DoAndReturn(handlerFunc.ServeDNS).AnyTimes()
				secondDnsServer.UnlockHandler()

				sd, err := NewDns(
					"test.com", uint32(80), backend.DNS_V4_ONLY,
					dnsServer.IpAddr(), secondDnsServer.IpAddr(),
				)
				Expect(err).ToNot(HaveOccurred())

				found := runDiscovery(sd)

				ipAddrs := <-found
				Expect(ipAddrs).To(HaveLen(2))
				Expect(ipAddrs[0].Ip).To(Or(Equal("10.0.0.1"), Equal("10.0.0.2")))
				Expect(ipAddrs[0].Port).To(Equal(uint32(80)))
				Expect(ipAddrs[1].Ip).To(Or(Equal("10.0.0.1"), Equal("10.0.0.2")))
				Expect(ipAddrs[1].Port).To(Equal(uint32(80)))
			})

		})
	})
})
