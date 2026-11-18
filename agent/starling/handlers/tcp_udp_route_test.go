package handlers_test

import (
	"context"
	"time"

	"github.com/cloudflare/ipvs"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	oerrors "github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/seagalia/flock/agent/starling/backendctl"
	"github.com/seagalia/flock/agent/starling/envoyctl"
	. "github.com/seagalia/flock/agent/starling/handlers"
	"github.com/seagalia/flock/agent/starling/ipvsctl"
	"github.com/seagalia/flock/agent/starling/testhelpers"
)

func tcpRouteConfig(backendUuid string, opts *starling_vsopts.TcpUdpOptions) *vs.TcpRouteConfig {
	rteConf := &vs.TcpRouteConfig{BackendUuid: backendUuid}
	if opts != nil {
		typedOpts, err := anypb.New(opts)
		Expect(err).ToNot(HaveOccurred())
		rteConf.TypedOptions = typedOpts
	}
	return rteConf
}

func tcpVs(uuid string, port uint32, rteConf *vs.TcpRouteConfig) *vs.VirtualServer {
	return &vs.VirtualServer{
		UUID:        uuid,
		Port:        port,
		RouteConfig: &vs.VirtualServer_TcpRouteConfig{TcpRouteConfig: rteConf},
	}
}

func udpVs(uuid string, port uint32) *vs.VirtualServer {
	return &vs.VirtualServer{UUID: uuid, Port: port, RouteConfig: &vs.VirtualServer_UdpRouteConfig{
		UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: "backend-1"},
	}}
}

func note(level flocktype.Note_Level, code, scope, message string) types.GomegaMatcher {
	return SatisfyAll(
		HaveField("Level", level),
		HaveField("Code", code),
		HaveField("Scope", scope),
		HaveField("Message", message),
	)
}

func listServices() []ipvs.Service {
	GinkgoHelper()
	services, err := ipvsctl.Services()
	Expect(err).ToNot(HaveOccurred())
	result := make([]ipvs.Service, len(services))
	for i, svc := range services {
		result[i] = svc.Service
	}
	return result
}

var _ = Describe("TcpUdpRouteHandler", func() {
	var (
		mockCtrl    *gomock.Controller
		mockNm      *testhelpers.MockNetworkManager
		mockJointer *testhelpers.MockJointer
		ctx         context.Context
		cancel      context.CancelFunc
		cw          *envoyctl.ConfigWatcher
		handler     *TcpUdpRouteHandler[*vs.TcpRouteConfig]
		aVip        *vip.VIP
		aBackend    *backend.Backend
		edpListener backendctl.EndpointListener
		states      *stores.StateStore
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockNm = testhelpers.NewMockNetworkManager(mockCtrl)
		mockJointer = testhelpers.NewMockJointer(mockCtrl)
		ctx, cancel = context.WithCancel(context.Background())
		cw = envoyctl.NewConfigWatcher(ctx, false, rt.Store, rt.States)
		cw.RunRequestQueues()
		states = stores.NewStateStore()
		handler = NewTcpRouteHandler(mockJointer, cw, mockNm, false, rt.Store, states)

		aVip = makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2)
		aBackend = makeBackend("backend-1")
		Expect(rt.Store.Backends().Create(context.Background(), aBackend)).To(Succeed())

		edpListener = nil
		mockJointer.EXPECT().AddListener(gomock.Any(), gomock.Any()).AnyTimes()
		mockJointer.EXPECT().RemoveListener(gomock.Any(), gomock.Any()).AnyTimes()
		mockJointer.EXPECT().AddEndpointListener("backend-1", gomock.Any()).Do(func(_ string, l backendctl.EndpointListener) {
			edpListener = l
		}).AnyTimes()
	})

	AfterEach(func() {
		cancel()
	})

	Context("with ipvs", func() {
		It("should create ipvs service with snat and masquerade by default", func() {
			mockNm.EXPECT().AddSnat(ipNet("10.0.0.1/32"), uint16(80), false, false).Return(nil)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			services := listServices()
			Expect(services).To(HaveLen(1))
			Expect(ipvsctl.ServiceToIpNet(services[0]).String()).To(Equal("10.0.0.1/32"))
			Expect(services[0].Port).To(Equal(uint16(80)))
			Expect(services[0].Protocol).To(Equal(ipvs.TCP))
			Expect(services[0].Scheduler).To(Equal("rr"))
			Expect(services[0].Timeout).To(Equal(uint32(5)))

			Expect(edpListener).To(BeAssignableToTypeOf(&IpvsHandler{}))
			edpListener.OnEndpointAdded(aBackend, &core.IPAddr{Ip: "192.168.0.1", Port: 8080})
			Expect(listDestinations(&services[0])).To(ConsistOf(
				destination{Addr: "192.168.0.1:8080", Weight: 1, FwdType: ipvs.Masquerade},
			))
		})

		It("should use direct route and no snat when disabled", func() {
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", &starling_vsopts.TcpUdpOptions{DisableSnat: true, DisableDnat: true}))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			services := listServices()
			edpListener.OnEndpointAdded(aBackend, &core.IPAddr{Ip: "192.168.0.1", Port: 8080})
			Expect(listDestinations(&services[0])).To(ConsistOf(
				destination{Addr: "192.168.0.1:8080", Weight: 1, FwdType: ipvs.DirectRoute},
			))
		})

		DescribeTable("should map lb policy to ipvs scheduler",
			func(policy backend.Backend_LbPolicy, scheduler string) {
				aBackend.LbPolicy = policy
				Expect(rt.Store.Backends().Update(context.Background(), "backend-1", aBackend)).To(Succeed())
				mockNm.EXPECT().AddSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
				virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
				Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
				Expect(listServices()[0].Scheduler).To(Equal(scheduler))
			},
			Entry("round robin", backend.Backend_ROUND_ROBIN, "rr"),
			Entry("weighted round robin", backend.Backend_WEIGHTED_ROUND_ROBIN, "wrr"),
			Entry("least request", backend.Backend_LEAST_REQUEST, "lc"),
			Entry("weighted least request", backend.Backend_WEIGHTED_LEAST_REQUEST, "wlc"),
			Entry("ring hash", backend.Backend_RING_HASH, "sh"),
			Entry("maglev", backend.Backend_MAGLEV, "mh"),
		)

		It("should update an already existing ipvs service", func() {
			mockNm.EXPECT().AddSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			Expect(listServices()).To(HaveLen(1))
		})

		It("should fail when snat can't be set", func() {
			mockNm.EXPECT().AddSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(context.DeadlineExceeded)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(MatchError(ContainSubstring("failed to add snat")))
			Expect(listServices()).To(BeEmpty())
		})

		It("should delete ipvs service and snat", func() {
			mockNm.EXPECT().AddSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			mockJointer.EXPECT().RemoveEndpointListener("backend-1", edpListener).Return(nil)
			mockNm.EXPECT().DelSnat(ipNet("10.0.0.1/32"), uint16(80), false, false).Return(nil)
			Expect(handler.DeleteRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			Expect(listServices()).To(BeEmpty())

			// deleting an unknown route is a no-op
			Expect(handler.DeleteRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
		})

		It("should switch destinations back to masquerade when dnat is enabled again", func() {
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", &starling_vsopts.TcpUdpOptions{DisableSnat: true, DisableDnat: true}))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			edpListener.OnEndpointAdded(aBackend, &core.IPAddr{Ip: "192.168.0.1", Port: 8080})

			mockNm.EXPECT().AddSnat(ipNet("10.0.0.1/32"), uint16(80), false, false).Return(nil)
			updatedVs := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.UpdateRoute(ctx, aVip, updatedVs, updatedVs.GetTcpRouteConfig())).To(Succeed())

			services := listServices()
			Expect(listDestinations(&services[0])).To(ConsistOf(
				destination{Addr: "192.168.0.1:8080", Weight: 1, FwdType: ipvs.Masquerade},
			))
		})

		It("should remove snat when disabled on update", func() {
			mockNm.EXPECT().AddSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			mockNm.EXPECT().DelSnat(ipNet("10.0.0.1/32"), uint16(80), false, false).Return(nil)
			updatedVs := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", &starling_vsopts.TcpUdpOptions{DisableSnat: true}))
			Expect(handler.UpdateRoute(ctx, aVip, updatedVs, updatedVs.GetTcpRouteConfig())).To(Succeed())
		})

		It("should move endpoint listener when backend changes", func() {
			Expect(rt.Store.Backends().Create(context.Background(), makeBackend("backend-2"))).To(Succeed())
			mockNm.EXPECT().AddSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			mockJointer.EXPECT().RemoveEndpointListener("backend-1", edpListener).Return(nil)
			mockJointer.EXPECT().AddEndpointListener("backend-2", edpListener)
			updatedVs := tcpVs("vs-1", 80, tcpRouteConfig("backend-2", nil))
			Expect(handler.UpdateRoute(ctx, aVip, updatedVs, updatedVs.GetTcpRouteConfig())).To(Succeed())
		})

		It("should ignore update of an unknown route", func() {
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.UpdateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			Expect(listServices()).To(BeEmpty())
		})

		It("should update ipvs scheduler when backend is updated", func() {
			mockNm.EXPECT().AddSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			aBackend.LbPolicy = backend.Backend_LEAST_REQUEST
			Expect(rt.Store.Backends().Update(context.Background(), "backend-1", aBackend)).To(Succeed())
			handler.OnBackendUpdated(aBackend)
			Expect(listServices()[0].Scheduler).To(Equal("lc"))

			// other backends are ignored
			handler.OnBackendUpdated(makeBackend("other"))
			handler.OnBackendAdded(aBackend)
			handler.OnBackendRemoved(aBackend)
		})

		It("should garbage collect ipvs services it does not manage", func() {
			mockNm.EXPECT().AddSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			orphan := ipvsService("10.0.0.99/32", 443)
			Expect(ipvsctl.CreateService(*orphan)).To(Succeed())
			udpService := ipvsService("10.0.0.98/32", 53)
			udpService.Protocol = ipvs.UDP
			Expect(ipvsctl.CreateService(*udpService)).To(Succeed())

			Expect(handler.GarbageCollect()).To(Succeed())
			services := listServices()
			remaining := make([]string, 0, len(services))
			for _, svc := range services {
				remaining = append(remaining, ipvsctl.ServiceToIpNet(svc).String())
			}
			Expect(remaining).To(ConsistOf("10.0.0.1/32", "10.0.0.98/32"), "udp services must be left to udp handler")
		})
	})

	Context("with envoy (proxy protocol enabled)", func() {
		var lds *envoyStub

		BeforeEach(func() {
			aBackend.EnableProxyProtocol = true
			aBackend.ProxyProtocolVersion = backend.Backend_V2
			Expect(rt.Store.Backends().Update(context.Background(), "backend-1", aBackend)).To(Succeed())
			lds = startEnvoyStub(ctx, cw, helpers.TypeUrl[*listenerv3.Listener]())
			startEnvoyStub(ctx, cw, "type.googleapis.com/envoy.config.cluster.v3.Cluster")
		})

		It("should create an envoy tcp listener instead of ipvs service", func() {
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(listServices()).To(BeEmpty())
			Expect(edpListener).To(BeIdenticalTo(cw))
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1"}))
		})

		It("should remove envoy listener on delete", func() {
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(HaveLen(1))

			mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(nil)
			Expect(handler.DeleteRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1", "-tcp-80/vip-1"}))
		})

		It("should remove envoy listener even when backend was already removed", func() {
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(HaveLen(1))

			mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(oerrors.NewErrNotFound("backend", "backend-1"))
			Expect(handler.DeleteRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1", "-tcp-80/vip-1"}))
		})

		It("should keep following the backend while another virtual server uses it", func() {
			vs1 := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			vs2 := tcpVs("vs-2", 81, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, vs1, vs1.GetTcpRouteConfig())).To(Succeed())
			Expect(handler.CreateRoute(ctx, aVip, vs2, vs2.GetTcpRouteConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(HaveLen(2))

			// no RemoveEndpointListener expected yet
			Expect(handler.DeleteRoute(ctx, aVip, vs1, vs1.GetTcpRouteConfig())).To(Succeed())

			mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(nil)
			Expect(handler.DeleteRoute(ctx, aVip, vs2, vs2.GetTcpRouteConfig())).To(Succeed())
		})

		It("should release the previous backend when the virtual server changes backend", func() {
			aBackend2 := makeBackend("backend-2")
			aBackend2.EnableProxyProtocol = true
			Expect(rt.Store.Backends().Create(context.Background(), aBackend2)).To(Succeed())
			mockJointer.EXPECT().AddEndpointListener("backend-2", cw)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(nil)
			updated := tcpVs("vs-1", 80, tcpRouteConfig("backend-2", nil))
			Expect(handler.UpdateRoute(ctx, aVip, updated, updated.GetTcpRouteConfig())).To(Succeed())

			mockJointer.EXPECT().RemoveEndpointListener("backend-2", cw).Return(nil)
			Expect(handler.DeleteRoute(ctx, aVip, updated, updated.GetTcpRouteConfig())).To(Succeed())
		})

		It("should move route from ipvs to envoy when proxy protocol gets enabled", func() {
			aBackend.EnableProxyProtocol = false
			Expect(rt.Store.Backends().Update(context.Background(), "backend-1", aBackend)).To(Succeed())
			mockNm.EXPECT().AddSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			Expect(listServices()).To(HaveLen(1))

			aBackend.EnableProxyProtocol = true
			Expect(rt.Store.Backends().Update(context.Background(), "backend-1", aBackend)).To(Succeed())
			mockJointer.EXPECT().RemoveEndpointListener("backend-1", gomock.Any()).Return(nil)
			mockNm.EXPECT().DelSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.UpdateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(listServices()).To(BeEmpty())
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1"}))
		})

		It("should serve a virtual server accepting proxy protocol with envoy, and go back to ipvs without it", func() {
			aBackend.EnableProxyProtocol = false
			Expect(rt.Store.Backends().Update(context.Background(), "backend-1", aBackend)).To(Succeed())
			accepting := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			accepting.GetTcpRouteConfig().AcceptProxyProtocol = true
			Expect(handler.CreateRoute(ctx, aVip, accepting, accepting.GetTcpRouteConfig())).To(Succeed())
			Expect(listServices()).To(BeEmpty())
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1"}))

			mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(nil)
			mockNm.EXPECT().AddSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			plain := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.UpdateRoute(ctx, aVip, plain, plain.GetTcpRouteConfig())).To(Succeed())
			Expect(listServices()).To(HaveLen(1))
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1", "-tcp-80/vip-1"}))
		})

		It("should clean envoy and ipvs routes without failing", func() {
			virtServ := tcpVs("vs-envoy", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(rt.Store.Backends().Create(context.Background(), makeBackend("backend-ipvs"))).To(Succeed())
			mockJointer.EXPECT().AddEndpointListener("backend-ipvs", gomock.Any())
			mockNm.EXPECT().AddSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			ipvsVs := tcpVs("vs-ipvs", 81, tcpRouteConfig("backend-ipvs", nil))
			Expect(handler.CreateRoute(ctx, aVip, ipvsVs, ipvsVs.GetTcpRouteConfig())).To(Succeed())

			mockJointer.EXPECT().RemoveEndpointListener(gomock.Any(), gomock.Any()).Return(nil).Times(2)
			mockNm.EXPECT().DelSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
			Expect(handler.Clean()).To(Succeed())
			Expect(listServices()).To(BeEmpty())
		})
	})

	Context("with envoy forced (dev mode)", func() {
		var lds *envoyStub

		BeforeEach(func() {
			lds = startEnvoyStub(ctx, cw, helpers.TypeUrl[*listenerv3.Listener]())
			startEnvoyStub(ctx, cw, "type.googleapis.com/envoy.config.cluster.v3.Cluster")
		})

		It("should serve a tcp route with envoy when the backend has no proxy protocol", func() {
			forcedHandler := NewTcpRouteHandler(mockJointer, cw, mockNm, true, rt.Store, states)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(forcedHandler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(listServices()).To(BeEmpty())
			Expect(edpListener).To(BeIdenticalTo(cw))
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1"}))
		})

		It("should serve a udp route with envoy when the backend has no proxy protocol", func() {
			forcedHandler := NewUdpRouteHandler(mockJointer, cw, mockNm, true, rt.Store, states)
			virtServ := &vs.VirtualServer{UUID: "vs-udp", Port: 53, RouteConfig: &vs.VirtualServer_UdpRouteConfig{
				UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: "backend-1"},
			}}
			Expect(forcedHandler.CreateRoute(ctx, aVip, virtServ, virtServ.GetUdpRouteConfig())).To(Succeed())

			Expect(listServices()).To(BeEmpty())
			Expect(edpListener).To(BeIdenticalTo(cw))
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"udp-53/vip-1"}))
		})

		It("should stay on envoy when the backend is updated", func() {
			forcedHandler := NewTcpRouteHandler(mockJointer, cw, mockNm, true, rt.Store, states)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(forcedHandler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			aBackend.LbPolicy = backend.Backend_LEAST_REQUEST
			Expect(rt.Store.Backends().Update(context.Background(), "backend-1", aBackend)).To(Succeed())
			Expect(forcedHandler.UpdateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(listServices()).To(BeEmpty())
		})
	})

	Context("data path notes", func() {
		notesOf := func(uuid string) []*flocktype.Note {
			return states.Notes(helpers.TypeUrl[*vs.VirtualServer](), uuid)
		}
		withProxyProtocol := func() {
			aBackend.EnableProxyProtocol = true
			aBackend.ProxyProtocolVersion = backend.Backend_V2
			Expect(rt.Store.Backends().Update(context.Background(), "backend-1", aBackend)).To(Succeed())
		}

		BeforeEach(func() {
			mockNm.EXPECT().AddSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			mockNm.EXPECT().DelSnat(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			mockJointer.EXPECT().RemoveEndpointListener(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		})

		It("notes an ipvs route", func() {
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(notesOf("vs-1")).To(ConsistOf(
				note(flocktype.Note_INFO, "starling.datapath.ipvs", "10.0.0.1:80/tcp", "served by IPVS in the kernel")))
		})

		It("notes direct routing", func() {
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", &starling_vsopts.TcpUdpOptions{DisableDnat: true}))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(notesOf("vs-1")).To(ConsistOf(note(flocktype.Note_INFO, "starling.datapath.ipvs", "10.0.0.1:80/tcp",
				"served by IPVS in the kernel with direct routing")))
		})

		It("notes envoy for a backend with the PROXY protocol", func() {
			withProxyProtocol()
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(notesOf("vs-1")).To(ConsistOf(note(flocktype.Note_INFO, "starling.datapath.envoy", "10.0.0.1:80/tcp",
				"served by envoy, the backend uses the PROXY protocol and IPVS can't send its header")))
		})

		It("notes envoy for a virtual server accepting the PROXY protocol", func() {
			rteConf := tcpRouteConfig("backend-1", nil)
			rteConf.AcceptProxyProtocol = true
			virtServ := tcpVs("vs-1", 80, rteConf)
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(notesOf("vs-1")).To(ConsistOf(note(flocktype.Note_INFO, "starling.datapath.envoy", "10.0.0.1:80/tcp",
				"served by envoy, the virtual server accepts the PROXY protocol and IPVS can't read its header")))
		})

		It("warns when a udp route leaves the kernel", func() {
			withProxyProtocol()
			udpHandler := NewUdpRouteHandler(mockJointer, cw, mockNm, false, rt.Store, states)
			virtServ := udpVs("vs-udp", 53)
			Expect(udpHandler.CreateRoute(ctx, aVip, virtServ, virtServ.GetUdpRouteConfig())).To(Succeed())

			Expect(notesOf("vs-udp")).To(ConsistOf(note(flocktype.Note_WARNING, "starling.datapath.envoy", "10.0.0.1:53/udp",
				"served by envoy, the backend uses the PROXY protocol and IPVS can't send its header")))
		})

		It("warns when direct routing is lost", func() {
			withProxyProtocol()
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", &starling_vsopts.TcpUdpOptions{DisableDnat: true}))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(notesOf("vs-1")).To(ConsistOf(note(flocktype.Note_WARNING, "starling.datapath.envoy", "10.0.0.1:80/tcp",
				"served by envoy, the backend uses the PROXY protocol and IPVS can't send its header, direct routing is not used")))
		})

		It("notes dev mode without warning", func() {
			forcedHandler := NewUdpRouteHandler(mockJointer, cw, mockNm, true, rt.Store, states)
			virtServ := udpVs("vs-udp", 53)
			Expect(forcedHandler.CreateRoute(ctx, aVip, virtServ, virtServ.GetUdpRouteConfig())).To(Succeed())

			Expect(notesOf("vs-udp")).To(ConsistOf(note(flocktype.Note_INFO, "starling.datapath.envoy", "10.0.0.1:53/udp",
				"served by envoy in dev mode")))
		})

		It("forgets the note on delete", func() {
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			Expect(handler.DeleteRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(notesOf("vs-1")).To(BeEmpty())
		})

		It("replaces the note when the route moves to envoy", func() {
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			withProxyProtocol()
			Expect(handler.UpdateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(notesOf("vs-1")).To(ConsistOf(note(flocktype.Note_INFO, "starling.datapath.envoy", "10.0.0.1:80/tcp",
				"served by envoy, the backend uses the PROXY protocol and IPVS can't send its header")))
		})

		It("keeps the note of the other VIP", func() {
			otherVip := makeVip("vip-2", "10.0.0.2", starling_announce.StarlingAnnounce_LAYER2)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			Expect(handler.CreateRoute(ctx, otherVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())
			Expect(handler.DeleteRoute(ctx, aVip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(notesOf("vs-1")).To(ConsistOf(
				note(flocktype.Note_INFO, "starling.datapath.ipvs", "10.0.0.2:80/tcp", "served by IPVS in the kernel")))
		})

		It("brackets an IPv6 scope", func() {
			v6Vip := makeVip("vip-6", "fd00::1", starling_announce.StarlingAnnounce_LAYER2)
			virtServ := tcpVs("vs-1", 80, tcpRouteConfig("backend-1", nil))
			Expect(handler.CreateRoute(ctx, v6Vip, virtServ, virtServ.GetTcpRouteConfig())).To(Succeed())

			Expect(notesOf("vs-1")).To(ConsistOf(
				note(flocktype.Note_INFO, "starling.datapath.ipvs", "[fd00::1]:80/tcp", "served by IPVS in the kernel")))
		})
	})

	Context("udp", func() {
		It("should create udp ipvs service", func() {
			udpHandler := NewUdpRouteHandler(mockJointer, cw, mockNm, false, rt.Store, states)
			mockNm.EXPECT().AddSnat(ipNet("10.0.0.1/32"), uint16(53), true, false).Return(nil)
			virtServ := &vs.VirtualServer{UUID: "vs-udp", Port: 53, RouteConfig: &vs.VirtualServer_UdpRouteConfig{
				UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: "backend-1"},
			}}
			Expect(udpHandler.CreateRoute(ctx, aVip, virtServ, virtServ.GetUdpRouteConfig())).To(Succeed())
			Expect(listServices()[0].Protocol).To(Equal(ipvs.UDP))
		})
	})
})
