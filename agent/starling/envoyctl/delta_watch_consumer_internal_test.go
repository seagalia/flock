package envoyctl

import (
	"context"
	"time"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/genproto/googleapis/rpc/status"
)

var _ = Describe("DeltaWatchConsumer", func() {
	It("should consume every responses like envoy would do in dev mode", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cw := NewConfigWatcher(ctx, false, rt.Store, rt.States)
		cw.RunRequestQueues()

		consumer := NewDeltaWatchConsumer(cw)
		done := make(chan struct{})
		go func() {
			defer close(done)
			Expect(consumer.Run(ctx)).To(Succeed())
		}()

		// listener depends on backend, being sent proves consumer acked backend and reopened watches
		virtServ := &vs.VirtualServer{UUID: "vs-1", Port: 80, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
			TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: "backend-1"},
		}}
		cw.AddUpdateTcpListener(&vip.VIP{UUID: "vip-1", Ip: "10.0.0.1"}, virtServ, "backend-1", nil)
		cw.AddUpdateBackend(tcpBackend("backend-1"))

		listenerKey := cw.makeKey(helpers.TypeUrl[*listenerv3.Listener](), "tcp-80/vip-1")
		Eventually(func() bool {
			_, sent := cw.typeSent.Load(listenerKey)
			return sent
		}, 10*time.Second).Should(BeTrue())

		cancel()
		Eventually(done, 5*time.Second).Should(BeClosed())
	})
})

var _ = Describe("RawDeltaResponse", func() {
	It("should build delta discovery response with resource and cache it", func() {
		request := &discovery.DeltaDiscoveryRequest{TypeUrl: helpers.TypeUrl[*clusterv3.Cluster]()}
		resp := &RawDeltaResponse{
			DeltaRequest:      request,
			SystemVersionInfo: "backend-1",
			Resource:          &clusterv3.Cluster{Name: "backend-1"},
			Signature:         "sig",
			NextVersionMap:    map[string]string{"backend-1": "sig"},
			Ctx:               context.Background(),
		}
		deltaResp, err := resp.GetDeltaDiscoveryResponse()
		Expect(err).ToNot(HaveOccurred())
		Expect(deltaResp.GetTypeUrl()).To(Equal(request.GetTypeUrl()))
		Expect(deltaResp.GetResources()).To(HaveLen(1))
		Expect(deltaResp.GetResources()[0].GetName()).To(Equal("backend-1"))
		Expect(deltaResp.GetResources()[0].GetVersion()).To(Equal("sig"))

		again, err := resp.GetDeltaDiscoveryResponse()
		Expect(err).ToNot(HaveOccurred())
		Expect(again).To(BeIdenticalTo(deltaResp))

		Expect(resp.GetDeltaRequest()).To(Equal(request))
		Expect(resp.GetNextVersionMap()).To(HaveKeyWithValue("backend-1", "sig"))
		Expect(resp.GetContext()).ToNot(BeNil())
		version, err := resp.GetSystemVersion()
		Expect(err).ToNot(HaveOccurred())
		Expect(version).To(Equal("backend-1"))
	})

	It("should build a removal response", func() {
		resp := &RawDeltaResponse{
			DeltaRequest:     &discovery.DeltaDiscoveryRequest{TypeUrl: helpers.TypeUrl[*clusterv3.Cluster]()},
			RemovedResources: []string{"backend-1"},
		}
		deltaResp, err := resp.GetDeltaDiscoveryResponse()
		Expect(err).ToNot(HaveOccurred())
		Expect(deltaResp.GetResources()).To(BeEmpty())
		Expect(deltaResp.GetRemovedResources()).To(Equal([]string{"backend-1"}))
	})
})

var _ = Describe("ServerCallbacks", func() {
	BeforeEach(func() {
		DeferCleanup(zerolog.SetGlobalLevel, zerolog.GlobalLevel())
	})

	DescribeTable("should never fail stream whatever the log level",
		func(level zerolog.Level) {
			zerolog.SetGlobalLevel(level)
			callbacks := NewServerCallbacks()
			request := &discovery.DeltaDiscoveryRequest{
				TypeUrl:                 helpers.TypeUrl[*clusterv3.Cluster](),
				ResourceNamesSubscribe:  []string{"backend-1"},
				InitialResourceVersions: map[string]string{"backend-1": "v1"},
				ErrorDetail:             &status.Status{Message: "nack"},
			}
			Expect(callbacks.OnDeltaStreamOpen(context.Background(), 1, request.GetTypeUrl())).To(Succeed())
			Expect(callbacks.OnStreamDeltaRequest(1, request)).To(Succeed())
			callbacks.OnStreamDeltaResponse(1, request, &discovery.DeltaDiscoveryResponse{TypeUrl: request.GetTypeUrl()})
			callbacks.OnDeltaStreamClosed(1, &corev3.Node{Id: "starling-agent"})
		},
		Entry("info", zerolog.InfoLevel),
		Entry("debug", zerolog.DebugLevel),
	)
})
