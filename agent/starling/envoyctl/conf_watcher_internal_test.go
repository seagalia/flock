package envoyctl

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	proxy_protocolv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/listener/proxy_protocol/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/stream/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
)

// envoyStub acks each response by opening a new watch, as envoy does
type envoyStub struct {
	mutex     sync.Mutex
	responses []*discovery.DeltaDiscoveryResponse
}

func startEnvoyStub(ctx context.Context, cw *ConfigWatcher, typeUrl string, initialVersions map[string]string) *envoyStub {
	stub := &envoyStub{}
	go func() {
		defer GinkgoRecover()
		request := &cache.DeltaRequest{TypeUrl: typeUrl, InitialResourceVersions: initialVersions}
		for {
			respChan := make(chan cache.DeltaResponse, 1)
			cancel, err := cw.CreateDeltaWatch(request, stream.NewDeltaSubscription(nil, nil, initialVersions, true), respChan)
			Expect(err).ToNot(HaveOccurred())
			select {
			case <-ctx.Done():
				cancel()
				return
			case resp := <-respChan:
				deltaResp, err := resp.GetDeltaDiscoveryResponse()
				Expect(err).ToNot(HaveOccurred())
				stub.mutex.Lock()
				stub.responses = append(stub.responses, deltaResp)
				stub.mutex.Unlock()
			}
			// only first request contains initial resource versions
			request = &cache.DeltaRequest{TypeUrl: typeUrl}
			initialVersions = nil
		}
	}()
	return stub
}

// startWarmingCdsStub acks a cluster only once eds received its load assignment, as envoy warming a cluster does
func startWarmingCdsStub(ctx context.Context, cw *ConfigWatcher, eds *envoyStub) *envoyStub {
	stub := &envoyStub{}
	go func() {
		defer GinkgoRecover()
		request := &cache.DeltaRequest{TypeUrl: helpers.TypeUrl[*clusterv3.Cluster]()}
		for {
			respChan := make(chan cache.DeltaResponse, 1)
			cancel, err := cw.CreateDeltaWatch(request, stream.NewDeltaSubscription(nil, nil, nil, true), respChan)
			Expect(err).ToNot(HaveOccurred())
			var deltaResp *discovery.DeltaDiscoveryResponse
			select {
			case <-ctx.Done():
				cancel()
				return
			case resp := <-respChan:
				deltaResp, err = resp.GetDeltaDiscoveryResponse()
				Expect(err).ToNot(HaveOccurred())
				stub.mutex.Lock()
				stub.responses = append(stub.responses, deltaResp)
				stub.mutex.Unlock()
			}
			for _, res := range deltaResp.GetResources() {
				for !slices.Contains(eds.ResourceNames(), res.GetName()) {
					select {
					case <-ctx.Done():
						return
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
		}
	}()
	return stub
}

func (s *envoyStub) Responses() []*discovery.DeltaDiscoveryResponse {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]*discovery.DeltaDiscoveryResponse{}, s.responses...)
}

// ResourceNames gives names of resources sent, prefixed by '-' when it was a removal
func (s *envoyStub) ResourceNames() []string {
	names := make([]string, 0)
	for _, resp := range s.Responses() {
		for _, res := range resp.GetResources() {
			names = append(names, res.GetName())
		}
		for _, removed := range resp.GetRemovedResources() {
			names = append(names, "-"+removed)
		}
	}
	return names
}

func lastResource[T proto.Message](stub *envoyStub, message T) T {
	GinkgoHelper()
	responses := stub.Responses()
	Expect(responses).ToNot(BeEmpty())
	resources := responses[len(responses)-1].GetResources()
	Expect(resources).To(HaveLen(1))
	return unmarshalAny(resources[0].GetResource(), message)
}

func tcpBackend(uuid string) *backend.Backend {
	return &backend.Backend{UUID: uuid, Name: uuid, LbPolicy: backend.Backend_ROUND_ROBIN}
}

var _ = Describe("ConfigWatcher", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		cw     *ConfigWatcher
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		cw = NewConfigWatcher(ctx, false, rt.Store, rt.States)
		cw.RunRequestQueues()
	})

	AfterEach(func() {
		cancel()
	})

	Context("backends", func() {
		It("should send backend to envoy", func() {
			cds := startEnvoyStub(ctx, cw, helpers.TypeUrl[*clusterv3.Cluster](), nil)
			cw.AddUpdateBackend(tcpBackend("backend-1"))

			Eventually(cds.ResourceNames, 5*time.Second).Should(Equal([]string{"backend-1"}))
			envoyCluster := lastResource(cds, &clusterv3.Cluster{})
			Expect(envoyCluster.GetName()).To(Equal("backend-1"))
			Expect(envoyCluster.GetType()).To(Equal(clusterv3.Cluster_EDS))
			Expect(cds.Responses()[0].GetTypeUrl()).To(Equal(helpers.TypeUrl[*clusterv3.Cluster]()))
			Expect(cds.Responses()[0].GetResources()[0].GetVersion()).ToNot(BeEmpty())
		})

		It("should wrap backend transport in proxy protocol when enabled", func() {
			cds := startEnvoyStub(ctx, cw, helpers.TypeUrl[*clusterv3.Cluster](), nil)
			aBackend := tcpBackend("backend-1")
			aBackend.EnableProxyProtocol = true
			aBackend.ProxyProtocolVersion = backend.Backend_V2
			cw.AddUpdateBackend(aBackend)

			Eventually(cds.ResourceNames, 5*time.Second).Should(HaveLen(1))
			Expect(lastResource(cds, &clusterv3.Cluster{}).GetTransportSocket().GetName()).To(Equal("envoy.transport_sockets.proxy_protocol"))
		})

		It("should not resend a backend envoy already knows", func() {
			envoyCluster, err := makeEnvoyCluster(tcpBackend("backend-1"), nil)
			Expect(err).ToNot(HaveOccurred())
			sig, err := helpers.MessageSignature(envoyCluster)
			Expect(err).ToNot(HaveOccurred())

			// envoy restarted with an already known version of the cluster
			cds := startEnvoyStub(ctx, cw, helpers.TypeUrl[*clusterv3.Cluster](), map[string]string{"backend-1": sig})
			lds := startEnvoyStub(ctx, cw, helpers.TypeUrl[*listenerv3.Listener](), nil)
			cw.AddUpdateBackend(tcpBackend("backend-1"))

			// a listener depending on backend must still be sent as backend is considered as sent
			virtServ := &vs.VirtualServer{UUID: "vs-1", Port: 80, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
				TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: "backend-1"},
			}}
			cw.AddUpdateTcpListener(&vip.VIP{UUID: "vip-1", Ip: "10.0.0.1"}, virtServ, "backend-1", nil)
			Eventually(lds.ResourceNames, 5*time.Second).Should(HaveLen(1))
			Expect(cds.Responses()).To(BeEmpty())
		})

		It("should send the next backend on the watch a known backend left unused", func() {
			envoyCluster, err := makeEnvoyCluster(tcpBackend("backend-1"), nil)
			Expect(err).ToNot(HaveOccurred())
			sig, err := helpers.MessageSignature(envoyCluster)
			Expect(err).ToNot(HaveOccurred())
			cds := startEnvoyStub(ctx, cw, helpers.TypeUrl[*clusterv3.Cluster](), map[string]string{"backend-1": sig})

			cw.AddUpdateBackend(tcpBackend("backend-1"))
			cw.AddUpdateBackend(tcpBackend("backend-2"))

			// well below the minute a watch is waited for
			Eventually(cds.ResourceNames, 2*time.Second).Should(Equal([]string{"backend-2"}))
		})

		It("should remove backend from envoy", func() {
			cds := startEnvoyStub(ctx, cw, helpers.TypeUrl[*clusterv3.Cluster](), nil)
			cw.AddUpdateBackend(tcpBackend("backend-1"))
			Eventually(cds.ResourceNames, 5*time.Second).Should(HaveLen(1))

			cw.OnBackendRemoved(tcpBackend("backend-1"))
			Eventually(cds.ResourceNames, 5*time.Second).Should(Equal([]string{"backend-1", "-backend-1"}))
		})

		Context("used by virtual servers", func() {
			var (
				aVip *vip.VIP
				vsA  *vs.VirtualServer
				vsB  *vs.VirtualServer
				cds  *envoyStub
			)

			BeforeEach(func() {
				aVip = &vip.VIP{UUID: "vip-uuid-1234", Ip: "10.0.0.1"}
				vsA = &vs.VirtualServer{UUID: "vs-a", Port: 80}
				vsB = &vs.VirtualServer{UUID: "vs-b", Port: 81}
				cds = startEnvoyStub(ctx, cw, helpers.TypeUrl[*clusterv3.Cluster](), nil)
			})

			It("should remove the backend from envoy with its last user", func() {
				cw.UseBackend(aVip, vsA, tcpBackend("backend-1"))
				cw.UseBackend(aVip, vsB, tcpBackend("backend-1"))
				Eventually(cds.ResourceNames, 5*time.Second).Should(ContainElement("backend-1"))

				Expect(cw.ReleaseBackend(aVip, vsA, "backend-1")).To(BeFalse())
				Consistently(cds.ResourceNames, 300*time.Millisecond).ShouldNot(ContainElement("-backend-1"))

				Expect(cw.ReleaseBackend(aVip, vsB, "backend-1")).To(BeTrue())
				Eventually(cds.ResourceNames, 5*time.Second).Should(ContainElement("-backend-1"))
			})

			It("should count a virtual server using the backend again as one user", func() {
				cw.UseBackend(aVip, vsA, tcpBackend("backend-1"))
				cw.UseBackend(aVip, vsA, tcpBackend("backend-1"))
				Expect(cw.ReleaseBackend(aVip, vsA, "backend-1")).To(BeTrue())
			})

			It("should keep the backend for a virtual server on another vip whose uuid starts alike", func() {
				otherVip := &vip.VIP{UUID: "vip-uuid-5678", Ip: "10.0.0.2"}
				cw.UseBackend(aVip, vsA, tcpBackend("backend-1"))
				cw.UseBackend(otherVip, vsA, tcpBackend("backend-1"))

				Expect(cw.ReleaseBackend(aVip, vsA, "backend-1")).To(BeFalse())
			})

			It("should not recreate a released backend on a late backend notification", func() {
				cw.UseBackend(aVip, vsA, tcpBackend("backend-1"))
				Eventually(cds.ResourceNames, 5*time.Second).Should(Equal([]string{"backend-1"}))
				Expect(cw.ReleaseBackend(aVip, vsA, "backend-1")).To(BeTrue())
				Eventually(cds.ResourceNames, 5*time.Second).Should(Equal([]string{"backend-1", "-backend-1"}))

				cw.OnBackendUpdated(tcpBackend("backend-1"))
				cw.OnBackendAdded(tcpBackend("backend-1"))
				Consistently(cds.ResourceNames, 300*time.Millisecond).Should(Equal([]string{"backend-1", "-backend-1"}))
			})
		})
	})

	Context("listeners", func() {
		var (
			aVip *vip.VIP
			lds  *envoyStub
			cds  *envoyStub
		)

		BeforeEach(func() {
			aVip = &vip.VIP{UUID: "vip-uuid-1234", Ip: "10.0.0.1"}
			lds = startEnvoyStub(ctx, cw, helpers.TypeUrl[*listenerv3.Listener](), nil)
			cds = startEnvoyStub(ctx, cw, helpers.TypeUrl[*clusterv3.Cluster](), nil)
		})

		It("should wait backend to be sent before sending a listener using it", func() {
			virtServ := &vs.VirtualServer{UUID: "vs-1", Port: 80, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
				TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: "backend-1"},
			}}
			cw.AddUpdateTcpListener(aVip, virtServ, "backend-1", nil)
			Consistently(lds.Responses, 300*time.Millisecond).Should(BeEmpty())

			cw.AddUpdateBackend(tcpBackend("backend-1"))
			Eventually(cds.ResourceNames, 5*time.Second).Should(HaveLen(1))
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-uuid-1234"}))

			listener := lastResource(lds, &listenerv3.Listener{})
			Expect(listener.GetAddress().GetSocketAddress().GetAddress()).To(Equal("10.0.0.1"))
			Expect(listener.GetListenerFilters()).To(BeEmpty())
		})

		It("should send a waiting listener quickly once its backend is sent", func() {
			virtServ := &vs.VirtualServer{UUID: "vs-wait", Port: 80, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
				TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: "backend-late"},
			}}
			cw.AddUpdateTcpListener(aVip, virtServ, "backend-late", nil)
			// listener waits its backend for a while, retries must not back off exponentially
			Consistently(lds.Responses, 4*time.Second).Should(BeEmpty())

			cw.AddUpdateBackend(tcpBackend("backend-late"))
			Eventually(cds.ResourceNames, 2*time.Second).Should(ContainElement("backend-late"))
			Eventually(lds.ResourceNames, 1500*time.Millisecond).Should(Equal([]string{"tcp-80/vip-uuid-1234"}))
		})

		It("should not block other listeners when a removed listener was waiting for its backend", func() {
			waiting := &vs.VirtualServer{UUID: "vs-stale", Port: 80, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
				TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: "backend-never"},
			}}
			cw.AddUpdateTcpListener(aVip, waiting, "backend-never", nil)
			// listener is removed while waiting a backend which will never come
			cw.RemoveListener(aVip, waiting)

			ready := &vs.VirtualServer{UUID: "vs-ready", Port: 81, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
				TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: "backend-ready"},
			}}
			cw.AddUpdateBackend(tcpBackend("backend-ready"))
			cw.AddUpdateTcpListener(aVip, ready, "backend-ready", nil)
			Eventually(lds.ResourceNames, 3*time.Second).Should(ContainElement("tcp-81/vip-uuid-1234"))
			Expect(lds.ResourceNames()).ToNot(ContainElement("tcp-80/vip-uuid-1234"))
		})

		It("should add original src/dst filters when snat/dnat are disabled", func() {
			virtServ := &vs.VirtualServer{UUID: "vs-1", Port: 53, RouteConfig: &vs.VirtualServer_UdpRouteConfig{
				UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: "backend-1"},
			}}
			cw.AddUpdateBackend(tcpBackend("backend-1"))
			cw.AddUpdateUdpListener(aVip, virtServ, "backend-1", &starling_vsopts.TcpUdpOptions{DisableSnat: true, DisableDnat: true})
			Eventually(lds.ResourceNames, 5*time.Second).Should(HaveLen(1))

			listener := lastResource(lds, &listenerv3.Listener{})
			Expect(listener.GetUdpListenerConfig()).ToNot(BeNil())
			Expect(listener.GetFilterChains()).To(BeEmpty())
			Expect(listener.GetListenerFilters()).To(HaveLen(3))
			Expect(listener.GetListenerFilters()[0].GetName()).To(Equal("envoy.filters.listener.original_src"))
			Expect(listener.GetListenerFilters()[1].GetName()).To(Equal("envoy.filters.listener.original_dst"))
			Expect(listener.GetListenerFilters()[2].GetName()).To(Equal("envoy.filters.udp_listener.udp_proxy"))
		})

		It("should send http listener", func() {
			virtServ := &vs.VirtualServer{UUID: "vs-http", Port: 80, RouteConfig: &vs.VirtualServer_HttpRoutesConfig{
				HttpRoutesConfig: &vs.HttpRoutesConfig{VirtualHosts: []*route.VirtualHost{{Domains: []string{"*"}}}},
			}}
			routes := []*HttpRoute{{Name: "vs-http-0", VirtualHost: virtServ.GetHttpRoutesConfig().GetVirtualHosts()[0]}}
			cw.AddUpdateHttpListener(aVip, virtServ, routes, &starling_vsopts.HttpOptions{})
			Eventually(lds.ResourceNames, 5*time.Second).Should(HaveLen(1))

			listener := lastResource(lds, &listenerv3.Listener{})
			Expect(listener.GetListenerFilters()[0].GetName()).To(Equal("envoy.filters.listener.tls_inspector"))
			httpManagerOfChain(listener.GetFilterChains()[0])
		})

		It("should add original src filter on every listener when snat is globally disabled", func() {
			cancel()
			ctx, cancel = context.WithCancel(context.Background())
			cw = NewConfigWatcher(ctx, true, rt.Store, rt.States)
			cw.RunRequestQueues()
			lds = startEnvoyStub(ctx, cw, helpers.TypeUrl[*listenerv3.Listener](), nil)

			startEnvoyStub(ctx, cw, helpers.TypeUrl[*clusterv3.Cluster](), nil)
			cw.AddUpdateBackend(tcpBackend("backend-1"))
			virtServ := &vs.VirtualServer{UUID: "vs-tls", Port: 443, RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{
				TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: []*vs.TlsTcpRouteConfig{{BackendUuid: "backend-1"}}},
			}}
			cw.AddUpdateTlsTcpListener(aVip, virtServ, []*vs.TlsTcpRouteConfig{
				{BackendUuid: "backend-1", Mode: &vs.TlsTcpRouteConfig_Tls{Tls: &core.TLS{ServerNames: []string{"a.local"}, CertificatesUuid: []string{"cert-1"}}}},
			}, nil)
			Eventually(lds.ResourceNames, 5*time.Second).Should(HaveLen(1))
			listener := lastResource(lds, &listenerv3.Listener{})
			Expect(listener.GetListenerFilters()).To(HaveLen(2))
			Expect(listener.GetListenerFilters()[1].GetName()).To(Equal("envoy.filters.listener.original_src"))
		})

		Context("with accept_proxy_protocol", func() {
			expectListenerFilters := func(names ...string) {
				Eventually(lds.ResourceNames, 5*time.Second).Should(HaveLen(1))
				listener := lastResource(lds, &listenerv3.Listener{})
				got := make([]string, 0, len(listener.GetListenerFilters()))
				for _, f := range listener.GetListenerFilters() {
					got = append(got, f.GetName())
				}
				Expect(got).To(HaveExactElements(names))
				pp := &proxy_protocolv3.ProxyProtocol{}
				Expect(listener.GetListenerFilters()[0].GetTypedConfig().UnmarshalTo(pp)).To(Succeed())
				Expect(pp.GetAllowRequestsWithoutProxyProtocol()).To(BeFalse())
			}

			It("puts the proxy protocol listener filter before the tls inspector on tls listeners", func() {
				virtServ := &vs.VirtualServer{UUID: "vs-tls-pp", Port: 443, RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{
					TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: []*vs.TlsTcpRouteConfig{{BackendUuid: "backend-1"}}, AcceptProxyProtocol: true},
				}}
				cw.AddUpdateBackend(tcpBackend("backend-1"))
				cw.AddUpdateTlsTcpListener(aVip, virtServ, virtServ.GetTlsTcpRoutesConfig().GetRoutes(), nil)
				expectListenerFilters("envoy.filters.listener.proxy_protocol", "envoy.filters.listener.tls_inspector")
			})

			It("does the same for http listeners", func() {
				virtServ := &vs.VirtualServer{UUID: "vs-http-pp", Port: 80, RouteConfig: &vs.VirtualServer_HttpRoutesConfig{
					HttpRoutesConfig: &vs.HttpRoutesConfig{VirtualHosts: []*route.VirtualHost{{Domains: []string{"*"}}}, AcceptProxyProtocol: true},
				}}
				routes := []*HttpRoute{{Name: "vs-http-pp-0", VirtualHost: virtServ.GetHttpRoutesConfig().GetVirtualHosts()[0]}}
				cw.AddUpdateHttpListener(aVip, virtServ, routes, &starling_vsopts.HttpOptions{})
				expectListenerFilters("envoy.filters.listener.proxy_protocol", "envoy.filters.listener.tls_inspector")
			})

			It("does the same for tcp listeners", func() {
				virtServ := &vs.VirtualServer{UUID: "vs-tcp-pp", Port: 5432, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
					TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: "backend-1", AcceptProxyProtocol: true},
				}}
				cw.AddUpdateBackend(tcpBackend("backend-1"))
				cw.AddUpdateTcpListener(aVip, virtServ, "backend-1", nil)
				expectListenerFilters("envoy.filters.listener.proxy_protocol")
			})

			It("has no proxy protocol filter by default", func() {
				virtServ := &vs.VirtualServer{UUID: "vs-tls-nopp", Port: 443, RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{
					TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: []*vs.TlsTcpRouteConfig{{BackendUuid: "backend-1"}}},
				}}
				cw.AddUpdateBackend(tcpBackend("backend-1"))
				cw.AddUpdateTlsTcpListener(aVip, virtServ, virtServ.GetTlsTcpRoutesConfig().GetRoutes(), nil)
				Eventually(lds.ResourceNames, 5*time.Second).Should(HaveLen(1))
				listener := lastResource(lds, &listenerv3.Listener{})
				Expect(listener.GetListenerFilters()).To(HaveLen(1))
				Expect(listener.GetListenerFilters()[0].GetName()).To(Equal("envoy.filters.listener.tls_inspector"))
			})
		})

		It("should remove listener", func() {
			virtServ := &vs.VirtualServer{UUID: "vs-1", Port: 53, RouteConfig: &vs.VirtualServer_UdpRouteConfig{
				UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: "backend-1"},
			}}
			cw.AddUpdateBackend(tcpBackend("backend-1"))
			cw.AddUpdateUdpListener(aVip, virtServ, "backend-1", nil)
			Eventually(lds.ResourceNames, 5*time.Second).Should(HaveLen(1))
			cw.RemoveListener(aVip, virtServ)
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"udp-53/vip-uuid-1234", "-udp-53/vip-uuid-1234"}))
		})

		Context("on a port shared by virtual servers", func() {
			var (
				https     *vs.VirtualServer
				httpsHost *route.VirtualHost
				tls       *vs.VirtualServer
			)

			// lastServerNames gives the server names of each filter chain of the last listener sent with this name
			lastServerNames := func(name string) func() [][]string {
				return func() [][]string {
					var names [][]string
					for _, resp := range lds.Responses() {
						for _, res := range resp.GetResources() {
							if res.GetName() != name {
								continue
							}
							listener := unmarshalAny(res.GetResource(), &listenerv3.Listener{})
							names = [][]string{}
							for _, chain := range listener.GetFilterChains() {
								names = append(names, chain.GetFilterChainMatch().GetServerNames())
							}
						}
					}
					return names
				}
			}

			BeforeEach(func() {
				httpsHost = &route.VirtualHost{
					Domains:    []string{"web.local"},
					TlsOptions: []*core.TLS{{ServerNames: []string{"web.local"}, CertificatesUuid: []string{"cert-1"}}},
				}
				https = &vs.VirtualServer{UUID: "vs-https", Port: 443, RouteConfig: &vs.VirtualServer_HttpRoutesConfig{
					HttpRoutesConfig: &vs.HttpRoutesConfig{VirtualHosts: []*route.VirtualHost{httpsHost}},
				}}
				tls = &vs.VirtualServer{UUID: "vs-tls", Port: 443, RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{
					TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: []*vs.TlsTcpRouteConfig{{
						BackendUuid: "backend-1",
						Mode:        &vs.TlsTcpRouteConfig_Passthrough{Passthrough: &vs.TlsPassthrough{ServerNames: []string{"git.local"}}},
					}}},
				}}
				cw.AddUpdateBackend(tcpBackend("backend-1"))
				cw.AddUpdateHttpListener(aVip, https, []*HttpRoute{{Name: "vs-https-0", VirtualHost: httpsHost}}, &starling_vsopts.HttpOptions{})
				cw.AddUpdateTlsTcpListener(aVip, tls, tls.GetTlsTcpRoutesConfig().GetRoutes(), nil)
			})

			It("sends one listener holding the filter chains of every virtual server", func() {
				Eventually(lastServerNames("tcp-443/vip-uuid-1234"), 5*time.Second).Should(Equal([][]string{{"web.local"}, {"git.local"}}))
				Expect(slices.Compact(lds.ResourceNames())).To(Equal([]string{"tcp-443/vip-uuid-1234"}))
			})

			It("keeps the listener for the others when one virtual server is removed", func() {
				Eventually(lastServerNames("tcp-443/vip-uuid-1234"), 5*time.Second).Should(HaveLen(2))

				cw.RemoveListener(aVip, https)
				Eventually(lastServerNames("tcp-443/vip-uuid-1234"), 5*time.Second).Should(Equal([][]string{{"git.local"}}))
				Expect(lds.ResourceNames()).ToNot(ContainElement("-tcp-443/vip-uuid-1234"))

				cw.RemoveListener(aVip, tls)
				Eventually(lds.ResourceNames, 5*time.Second).Should(ContainElement("-tcp-443/vip-uuid-1234"))
			})

			It("moves a virtual server changing port out of the shared listener", func() {
				Eventually(lastServerNames("tcp-443/vip-uuid-1234"), 5*time.Second).Should(HaveLen(2))

				moved := proto.Clone(https).(*vs.VirtualServer)
				moved.Port = 8443
				cw.RemoveListener(aVip, https)
				cw.AddUpdateHttpListener(aVip, moved, []*HttpRoute{{Name: "vs-https-0", VirtualHost: httpsHost}}, &starling_vsopts.HttpOptions{})

				Eventually(lastServerNames("tcp-443/vip-uuid-1234"), 5*time.Second).Should(Equal([][]string{{"git.local"}}))
				Eventually(lastServerNames("tcp-8443/vip-uuid-1234"), 5*time.Second).Should(Equal([][]string{{"web.local"}}))
			})

			It("gives each vip its own listener even when their uuids start alike", func() {
				lookAlike := &vip.VIP{UUID: "vip-uuid-5678", Ip: "10.0.0.2"}
				cw.AddUpdateTlsTcpListener(lookAlike, tls, tls.GetTlsTcpRoutesConfig().GetRoutes(), nil)

				Eventually(lastServerNames("tcp-443/vip-uuid-5678"), 5*time.Second).Should(Equal([][]string{{"git.local"}}))
				Eventually(lastServerNames("tcp-443/vip-uuid-1234"), 5*time.Second).Should(Equal([][]string{{"web.local"}, {"git.local"}}))
			})

			It("gives each vip its own listener on the same port", func() {
				otherVip := &vip.VIP{UUID: "other-uuid", Ip: "10.0.0.2"}
				cw.AddUpdateTlsTcpListener(otherVip, tls, tls.GetTlsTcpRoutesConfig().GetRoutes(), nil)
				Eventually(lastServerNames("tcp-443/other-uuid"), 5*time.Second).Should(Equal([][]string{{"git.local"}}))
				Eventually(lastServerNames("tcp-443/vip-uuid-1234"), 5*time.Second).Should(HaveLen(2))
				sentForVip := len(lds.Responses())

				cw.RemoveListener(otherVip, tls)
				Eventually(lds.ResourceNames, 5*time.Second).Should(ContainElement("-tcp-443/other-uuid"))
				Consistently(func() []string { return lds.ResourceNames()[sentForVip:] }, 300*time.Millisecond).ShouldNot(ContainElement("tcp-443/vip-uuid-1234"))
			})
		})
	})

	Context("endpoints", func() {
		var (
			eds      *envoyStub
			aBackend *backend.Backend
		)

		lastEndpoints := func() []string {
			cla := lastResource(eds, &endpointv3.ClusterLoadAssignment{})
			addrs := make([]string, 0, len(cla.GetEndpoints()[0].GetLbEndpoints()))
			for _, lbEndpoint := range cla.GetEndpoints()[0].GetLbEndpoints() {
				sockAddr := lbEndpoint.GetEndpoint().GetAddress().GetSocketAddress()
				addrs = append(addrs, fmt.Sprintf("%s:%d", sockAddr.GetAddress(), sockAddr.GetPortValue()))
			}
			return addrs
		}

		BeforeEach(func() {
			aBackend = tcpBackend("backend-1")
			startEnvoyStub(ctx, cw, helpers.TypeUrl[*clusterv3.Cluster](), nil)
			eds = startEnvoyStub(ctx, cw, helpers.TypeUrl[*endpointv3.ClusterLoadAssignment](), nil)
			cw.AddUpdateBackend(aBackend)
		})

		It("should send endpoints on startup and follow added/removed endpoints", func() {
			cw.OnStartup(aBackend, []*core.IPAddr{{Ip: "10.0.0.1", Port: 80}})
			Eventually(func() int { return len(eds.Responses()) }, 5*time.Second).Should(Equal(1))
			Expect(lastEndpoints()).To(ConsistOf("10.0.0.1:80"))

			cw.OnEndpointAdded(aBackend, &core.IPAddr{Ip: "10.0.0.2", Port: 80})
			Eventually(func() int { return len(eds.Responses()) }, 5*time.Second).Should(Equal(2))
			Expect(lastEndpoints()).To(ConsistOf("10.0.0.1:80", "10.0.0.2:80"))

			cw.OnEndpointRemoved(aBackend, &core.IPAddr{Ip: "10.0.0.1", Port: 80})
			Eventually(func() int { return len(eds.Responses()) }, 5*time.Second).Should(Equal(3))
			Expect(lastEndpoints()).To(ConsistOf("10.0.0.2:80"))
		})

		It("should drop previous endpoints when restarting without endpoints", func() {
			cw.OnStartup(aBackend, []*core.IPAddr{{Ip: "10.0.0.1", Port: 80}})
			Eventually(func() int { return len(eds.Responses()) }, 5*time.Second).Should(Equal(1))

			// resolver restarted (backend updated) without any healthy endpoint yet
			cw.OnStartup(aBackend, []*core.IPAddr{})
			cw.OnEndpointAdded(aBackend, &core.IPAddr{Ip: "10.0.0.9", Port: 80})
			Eventually(func() int { return len(eds.Responses()) }, 5*time.Second).Should(Equal(2))
			Expect(lastEndpoints()).To(ConsistOf("10.0.0.9:80"))
		})

		It("should keep endpoints of backends separated under concurrent updates", func() {
			otherBackend := tcpBackend("backend-2")
			cw.AddUpdateBackend(otherBackend)
			wg := &sync.WaitGroup{}
			for i := range 20 {
				wg.Add(2)
				go func(i int) {
					defer wg.Done()
					cw.OnEndpointAdded(aBackend, &core.IPAddr{Ip: "10.0.1.1", Port: uint32(1000 + i)})
				}(i)
				go func(i int) {
					defer wg.Done()
					cw.OnEndpointAdded(otherBackend, &core.IPAddr{Ip: "10.0.2.1", Port: uint32(1000 + i)})
				}(i)
			}
			wg.Wait()
			Expect(cw.constructIpAddresses("backend-1")).To(HaveLen(20))
			Expect(cw.constructIpAddresses("backend-2")).To(HaveLen(20))
		})
	})

	Context("backends warming in envoy", func() {
		var (
			cds *envoyStub
			eds *envoyStub
		)

		BeforeEach(func() {
			eds = startEnvoyStub(ctx, cw, helpers.TypeUrl[*endpointv3.ClusterLoadAssignment](), nil)
			cds = startWarmingCdsStub(ctx, cw, eds)
		})

		It("sends an empty load assignment to a new backend without healthy endpoints", func() {
			cw.AddUpdateBackend(tcpBackend("backend-down"))
			cw.AddUpdateBackend(tcpBackend("backend-next"))
			cw.OnStartup(tcpBackend("backend-down"), []*core.IPAddr{})
			cw.OnStartup(tcpBackend("backend-next"), []*core.IPAddr{{Ip: "10.0.0.2", Port: 80}})

			Eventually(cds.ResourceNames, 3*time.Second).Should(Equal([]string{"backend-down", "backend-next"}))
			Eventually(eds.ResourceNames, time.Second).Should(Equal([]string{"backend-down", "backend-next"}))
			firstResource := eds.Responses()[0].GetResources()[0].GetResource()
			downEndpoints := unmarshalAny(firstResource, &endpointv3.ClusterLoadAssignment{}).GetEndpoints()[0].GetLbEndpoints()
			Expect(downEndpoints).To(BeEmpty())
		})

		It("does not hold a load assignment behind one waiting for its backend", func() {
			cw.AddUpdateBackend(tcpBackend("backend-first"))
			cw.AddUpdateBackend(tcpBackend("backend-second"))
			// the load assignment of backend-second comes first, but envoy accepts backend-second only
			// once backend-first got its load assignment
			cw.OnStartup(tcpBackend("backend-second"), []*core.IPAddr{{Ip: "10.0.0.2", Port: 80}})
			cw.OnStartup(tcpBackend("backend-first"), []*core.IPAddr{{Ip: "10.0.0.1", Port: 80}})

			Eventually(eds.ResourceNames, 3*time.Second).Should(ConsistOf("backend-first", "backend-second"))
			Eventually(cds.ResourceNames, time.Second).Should(Equal([]string{"backend-first", "backend-second"}))
		})

		It("drops a load assignment whose backend is never sent, without blocking the next ones", func() {
			cw.loadAssignmentWaitLimit = 200 * time.Millisecond
			cw.OnStartup(tcpBackend("backend-never"), []*core.IPAddr{{Ip: "10.0.0.9", Port: 80}})
			// past the limit, the load assignment would block the queue head if it was not dropped
			time.Sleep(500 * time.Millisecond)

			cw.AddUpdateBackend(tcpBackend("backend-later"))
			cw.OnStartup(tcpBackend("backend-later"), []*core.IPAddr{{Ip: "10.0.0.1", Port: 80}})
			Eventually(eds.ResourceNames, time.Second).Should(Equal([]string{"backend-later"}))
		})
	})

	Context("certificates", func() {
		It("should send and remove certificate as secret", func() {
			sds := startEnvoyStub(ctx, cw, helpers.TypeUrl[*tlsv3.Secret](), nil)
			cw.AddUpdateCertificate(&cert.Certificate{UUID: "cert-1", CertChain: "chain-pem", PrivateKey: "key-pem"})

			Eventually(sds.ResourceNames, 5*time.Second).Should(Equal([]string{"cert-1"}))
			secret := lastResource(sds, &tlsv3.Secret{})
			Expect(secret.GetTlsCertificate().GetCertificateChain().GetInlineString()).To(Equal("chain-pem"))
			Expect(secret.GetTlsCertificate().GetPrivateKey().GetInlineString()).To(Equal("key-pem"))

			cw.RemoveCertificate("cert-1")
			Eventually(sds.ResourceNames, 5*time.Second).Should(Equal([]string{"cert-1", "-cert-1"}))
		})
	})

	Context("subscriptions", func() {
		It("should resend a resource envoy subscribes to after it was sent", func() {
			secretType := helpers.TypeUrl[*tlsv3.Secret]()
			first := make(chan cache.DeltaResponse, 1)
			Expect(cw.CreateDeltaWatch(&cache.DeltaRequest{TypeUrl: secretType}, stream.NewDeltaSubscription(nil, nil, nil, false), first)).Error().ToNot(HaveOccurred())
			// certificate is sent before any listener needs it, envoy drops it as nothing subscribed to it yet
			cw.AddUpdateCertificate(&cert.Certificate{UUID: "cert-early", CertChain: "chain", PrivateKey: "key"})
			Eventually(first, 3*time.Second).Should(Receive())

			// a listener now needs it, envoy subscribes to it by name
			second := make(chan cache.DeltaResponse, 1)
			Expect(cw.CreateDeltaWatch(&cache.DeltaRequest{
				TypeUrl:                secretType,
				ResourceNamesSubscribe: []string{"cert-early", "cert-unknown"},
			}, stream.NewDeltaSubscription(nil, nil, nil, false), second)).Error().ToNot(HaveOccurred())

			var resp cache.DeltaResponse
			Eventually(second, 3*time.Second).Should(Receive(&resp))
			deltaResp, err := resp.GetDeltaDiscoveryResponse()
			Expect(err).ToNot(HaveOccurred())
			Expect(deltaResp.GetResources()).To(HaveLen(1))
			Expect(deltaResp.GetResources()[0].GetName()).To(Equal("cert-early"))
		})
	})

	Context("envoy errors", func() {
		It("should store an envoy rejection of a shared listener on every virtual server of it", func() {
			aVip := &vip.VIP{UUID: "vip-uuid-1234", Ip: "10.0.0.1"}
			first := &vs.VirtualServer{UUID: "vs-first", Port: 443, RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{
				TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: []*vs.TlsTcpRouteConfig{{
					BackendUuid: "backend-1",
					Mode:        &vs.TlsTcpRouteConfig_Passthrough{Passthrough: &vs.TlsPassthrough{ServerNames: []string{"a.local"}}},
				}}},
			}}
			second := proto.Clone(first).(*vs.VirtualServer)
			second.UUID = "vs-second"
			second.GetTlsTcpRoutesConfig().GetRoutes()[0].GetPassthrough().ServerNames = []string{"b.local"}
			Expect(rt.Store.VirtualServers().Create(context.Background(), first)).To(Succeed())
			Expect(rt.Store.VirtualServers().Create(context.Background(), second)).To(Succeed())
			cw.AddUpdateTlsTcpListener(aVip, first, first.GetTlsTcpRoutesConfig().GetRoutes(), nil)
			cw.AddUpdateTlsTcpListener(aVip, second, second.GetTlsTcpRoutesConfig().GetRoutes(), nil)

			// envoy nack the listener
			Expect(cw.CreateDeltaWatch(&cache.DeltaRequest{
				TypeUrl:     helpers.TypeUrl[*listenerv3.Listener](),
				ErrorDetail: &status.Status{Message: "invalid listener"},
			}, stream.NewDeltaSubscription(nil, nil, map[string]string{"tcp-443/vip-uuid-1234": "v1"}, true), make(chan cache.DeltaResponse, 1))).Error().ToNot(HaveOccurred())

			for _, uuid := range []string{"vs-first", "vs-second"} {
				_, _, err := rt.States.Get(helpers.TypeUrl[*vs.VirtualServer](), uuid)
				Expect(err).To(MatchError(ContainSubstring("invalid listener")), uuid)
			}

			cw.RemoveListener(aVip, first)
			_, _, err := rt.States.Get(helpers.TypeUrl[*vs.VirtualServer](), "vs-first")
			Expect(err).ToNot(HaveOccurred())
		})

		It("should store envoy rejection on the flock resource and clear it on next successful send", func() {
			aBackend := tcpBackend("backend-in-error")
			Expect(rt.Store.Backends().Create(context.Background(), aBackend)).To(Succeed())

			// envoy nack the cluster
			Expect(cw.CreateDeltaWatch(&cache.DeltaRequest{
				TypeUrl:     helpers.TypeUrl[*clusterv3.Cluster](),
				ErrorDetail: &status.Status{Message: "invalid backend"},
			}, stream.NewDeltaSubscription(nil, nil, map[string]string{"backend-in-error": "v1"}, true), make(chan cache.DeltaResponse, 1))).Error().ToNot(HaveOccurred())

			_, _, err := rt.States.Get(helpers.TypeUrl[*backend.Backend](), "backend-in-error")
			Expect(err).To(MatchError(ContainSubstring("invalid backend")))

			startEnvoyStub(ctx, cw, helpers.TypeUrl[*clusterv3.Cluster](), nil)
			cw.AddUpdateBackend(aBackend)
			Eventually(func() error {
				_, _, err := rt.States.Get(helpers.TypeUrl[*backend.Backend](), "backend-in-error")
				return err
			}, 5*time.Second).Should(Succeed())
		})
	})
})

var _ = Describe("ConfigWatcher stop", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		cw     *ConfigWatcher
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		DeferCleanup(func() { cancel() })
		cw = NewConfigWatcher(ctx, false, rt.Store, rt.States)
	})

	It("should never send to a watch go-control-plane cancelled", func() {
		cw.RunRequestQueues()
		typeUrl := helpers.TypeUrl[*clusterv3.Cluster]()
		key := cw.clusterKey("backend-race")
		responders := &sync.WaitGroup{}
		cancellers := &sync.WaitGroup{}
		for range 1000 {
			// go-control-plane closes the response channel right after cancelling the watch of a closed stream
			responses := make(chan cache.DeltaResponse, 1)
			cancelWatch, err := cw.CreateDeltaWatch(&cache.DeltaRequest{TypeUrl: typeUrl}, stream.NewDeltaSubscription(nil, nil, nil, true), responses)
			Expect(err).ToNot(HaveOccurred())
			cancellers.Go(func() {
				defer GinkgoRecover()
				cancelWatch()
				close(responses)
			})
			responders.Go(func() {
				defer GinkgoRecover()
				cw.requestDelete(key)
			})
		}
		cancellers.Wait()
		// the responders that found no watch left wait for one, until the stop
		cancel()
		done := make(chan struct{})
		go func() {
			responders.Wait()
			close(done)
		}()
		Eventually(done, 5*time.Second).Should(BeClosed())
	})

	It("should keep the watch of a new stream when the watch of the old one is cancelled", func() {
		typeUrl := helpers.TypeUrl[*clusterv3.Cluster]()
		request := &cache.DeltaRequest{TypeUrl: typeUrl}
		cancelOld, err := cw.CreateDeltaWatch(request, stream.NewDeltaSubscription(nil, nil, nil, true), make(chan cache.DeltaResponse, 1))
		Expect(err).ToNot(HaveOccurred())
		newResponses := make(chan cache.DeltaResponse, 1)
		_, err = cw.CreateDeltaWatch(request, stream.NewDeltaSubscription(nil, nil, nil, true), newResponses)
		Expect(err).ToNot(HaveOccurred())

		cancelOld()
		watch, err := cw.retrieveDeltaWatch(typeUrl)
		Expect(err).ToNot(HaveOccurred())
		isNewWatch := watch.responses == (chan<- cache.DeltaResponse)(newResponses)
		Expect(isNewWatch).To(BeTrue())
	})

	It("should stop sending a request when its queue is full and the watcher stops", func() {
		key := cw.clusterKey("backend-full")
		for range cap(cw.retrieveElemRequestChan(key.typeUrl)) {
			cw.sendElemRequest(key, false)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			cw.sendElemRequest(key, false)
		}()
		Consistently(done, 100*time.Millisecond).ShouldNot(BeClosed())
		cancel()
		Eventually(done, time.Second).Should(BeClosed())
	})

	It("should stop waiting for an element when the watcher stops", func() {
		owner := cw.makeKey(helpers.TypeUrl[*listenerv3.Listener](), "listener-waiting")
		cw.typedCache.Store(owner, &listenerv3.Listener{Name: "listener-waiting"})
		done := make(chan error, 1)
		go func() { done <- cw.waitElem(owner, cw.clusterKey("backend-never-sent")) }()
		Consistently(done, 200*time.Millisecond).ShouldNot(Receive())
		cancel()
		Eventually(done, time.Second).Should(Receive(HaveOccurred()))
	})
})
