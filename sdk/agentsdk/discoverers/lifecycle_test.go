package discoverers_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/vigie/sdk/clients"
	eds "github.com/seagalia/vigie/sdk/vigie/eds/v1"
	"github.com/seagalia/vigie/sdk/vigie/eds/v1/edsconnect"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/durationpb"

	. "github.com/seagalia/flock/sdk/agentsdk/discoverers"
)

const (
	// blockedSendSettle lets a Run reach its send on found before the ctx is cancelled
	blockedSendSettle = 100 * time.Millisecond
	// returnBudget is how long a Run may take to return once its ctx is cancelled
	returnBudget = time.Second
)

var _ = Describe("Run on a cancelled context", func() {
	// runUntilCancel runs d with a found channel nobody reads, cancels ctx once Run had time to block on its send and
	// gives what Run returned.
	runUntilCancel := func(d Discoverer) error {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := make(chan error, 1)
		go func() {
			result <- d.Run(ctx, make(chan []*core.IPAddr))
		}()
		time.Sleep(blockedSendSettle)
		cancel()
		var err error
		Eventually(result, returnBudget).Should(Receive(&err))
		return err
	}

	It("returns from the static discoverer", func() {
		err := runUntilCancel(NewStatic([]*core.IPAddr{{Ip: "127.0.0.1", Port: 80}}))
		Expect(err).To(MatchError(context.Canceled))
	})

	It("returns from the http eds discoverer", func() {
		server := ghttp.NewServer()
		defer server.Close()
		server.AppendHandlers(ghttp.RespondWith(http.StatusOK, `[{"ip":"127.0.0.1","port":80}]`))
		u, err := core.UrlToCoreUrl(server.URL())
		Expect(err).ToNot(HaveOccurred())
		sd, err := NewEds(u, "web", "", nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(runUntilCancel(sd)).To(MatchError(context.Canceled))
	})

	It("returns from the grpc eds discoverer", func() {
		mockCtrl := gomock.NewController(GinkgoT())
		client := testhelpers.NewMockEndpointDiscoveryServiceClient(mockCtrl)
		SetGrpcEndpointDiscoClientFactory(func(string, clients.Options) (edsconnect.EndpointDiscoveryServiceClient, error) {
			return client, nil
		})
		client.EXPECT().GetEndpoints(gomock.Any(), gomock.Any()).
			Return(&eds.GetEndpointsResponse{Endpoints: []*eds.Endpoint{{Ip: "127.0.0.1", Port: 80}}}, nil)
		sd, err := NewEds(&core.URL{Scheme: "grpc", Host: "vigie", Port: 8080}, "web", "", nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(runUntilCancel(sd)).To(MatchError(context.Canceled))
	})

	It("returns from a dns lookup against a server that never answers", func() {
		conn, err := net.ListenPacket("udp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		defer conn.Close()
		addr := conn.LocalAddr().(*net.UDPAddr)
		server := &core.IPAddr{Ip: "127.0.0.1", Port: uint32(addr.Port)}
		sd, err := NewDns("test.com", 80, backend.DNS_V4_ONLY, server)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(ClearCacheDns)

		Expect(runUntilCancel(sd)).To(MatchError(context.Canceled))
	})
})

var _ = Describe("Refresh after an error", func() {
	It("records the error, keeps ticking and sends the next result", func() {
		mockCtrl := gomock.NewController(GinkgoT())
		mockDisco := testhelpers.NewMockDiscoverer(mockCtrl)
		states := stores.NewStateStore()
		targets := []*core.IPAddr{{Ip: "127.0.0.1", Port: 80}}
		gomock.InOrder(
			mockDisco.EXPECT().Run(gomock.Any(), gomock.Any()).Return(errDnsDown),
			mockDisco.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, found chan<- []*core.IPAddr) error {
					found <- targets
					return nil
				}),
		)
		mockDisco.EXPECT().Run(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

		backendDef := &backend.Backend{UUID: "backend-retry", RefreshRate: durationpb.New(20 * time.Millisecond)}
		sd := NewRefresh(backendDef, mockDisco, states)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		found := make(chan []*core.IPAddr)
		result := make(chan error, 1)
		go func() {
			result <- sd.Run(ctx, found)
		}()

		Eventually(func() error {
			_, _, err := states.Get(helpers.TypeUrl[*backend.Backend](), "backend-retry")
			return err
		}).Should(MatchError(errDnsDown))
		Eventually(found).Should(Receive(Equal(targets)))
		cancel()
		Eventually(result, returnBudget).Should(Receive(BeNil()))
	})
})

var errDnsDown = errors.New("dns down")

var _ = Describe("Dns with a cached answer", func() {
	var (
		dnsServer *DNSServer
		sd        *Dns
		cached    []*core.IPAddr
	)
	BeforeEach(func() {
		mockCtrl := gomock.NewController(GinkgoT())
		handler := testhelpers.NewMockDnsHandler(mockCtrl)
		dnsServer = NewDNSServer(handler)
		ready := make(chan struct{})
		go dnsServer.Run(ready)
		<-ready
		DeferCleanup(dnsServer.Stop)
		DeferCleanup(ClearCacheDns)

		cached = []*core.IPAddr{{Ip: "10.0.0.1"}}
		answering := NewEasyHandler("test.com", cached, false)
		silent := func(dns.ResponseWriter, *dns.Msg) {}
		dnsServer.LockHandler()
		// the first query fills the cache, the server never answers after it
		handler.EXPECT().ServeDNS(gomock.Any(), gomock.Any()).DoAndReturn(answering.ServeDNS).Times(1)
		handler.EXPECT().ServeDNS(gomock.Any(), gomock.Any()).DoAndReturn(silent).AnyTimes()
		dnsServer.UnlockHandler()

		var err error
		sd, err = NewDns("test.com", 80, backend.DNS_V4_ONLY, dnsServer.IpAddr())
		Expect(err).ToNot(HaveOccurred())
		found := make(chan []*core.IPAddr, 1)
		Expect(sd.Run(context.Background(), found)).To(Succeed())
		Expect(found).To(Receive(HaveLen(1)))
	})

	It("answers from the cache when the server times out", func() {
		found := make(chan []*core.IPAddr, 1)
		Expect(sd.Run(context.Background(), found)).To(Succeed())
		var ips []*core.IPAddr
		Expect(found).To(Receive(&ips))
		Expect(ips).To(HaveLen(1))
		Expect(ips[0].GetIp()).To(Equal("10.0.0.1"))
	})

	It("returns the cancel of the caller instead of the cached answer", func() {
		found := make(chan []*core.IPAddr, 1)
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(blockedSendSettle, cancel)

		Expect(sd.Run(ctx, found)).To(MatchError(context.Canceled))
		Expect(found).ToNot(Receive())
	})
})
