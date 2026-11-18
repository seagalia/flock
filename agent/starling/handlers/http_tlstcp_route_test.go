package handlers_test

import (
	"context"
	"time"

	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	ext_procv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	http_connection_managerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	tcp_proxyv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/tcp_proxy/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	oerrors "github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/limit/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	starling_limit "github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	starling_route "github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/seagalia/flock/agent/starling/envoyctl"
	. "github.com/seagalia/flock/agent/starling/handlers"
	"github.com/seagalia/flock/agent/starling/testhelpers"
	wafclient "github.com/seagalia/flock/agent/starling/waf/client"
)

func httpManagerOfListener(listener *listenerv3.Listener) *http_connection_managerv3.HttpConnectionManager {
	GinkgoHelper()
	httpMan := &http_connection_managerv3.HttpConnectionManager{}
	Expect(listener.GetFilterChains()[0].GetFilters()[0].GetTypedConfig().UnmarshalTo(httpMan)).To(Succeed())
	return httpMan
}

var _ = Describe("Envoy route handlers", func() {
	var (
		mockCtrl    *gomock.Controller
		mockJointer *testhelpers.MockJointer
		ctx         context.Context
		cancel      context.CancelFunc
		cw          *envoyctl.ConfigWatcher
		lds         *envoyStub
		clusters    *envoyStub
		aVip        *vip.VIP
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockJointer = testhelpers.NewMockJointer(mockCtrl)
		ctx, cancel = context.WithCancel(context.Background())
		cw = envoyctl.NewConfigWatcher(ctx, false, rt.Store, rt.States)
		cw.RunRequestQueues()
		lds = startEnvoyStub(ctx, cw, helpers.TypeUrl[*listenerv3.Listener]())
		clusters = startEnvoyStub(ctx, cw, "type.googleapis.com/envoy.config.cluster.v3.Cluster")
		aVip = makeVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2)

		Expect(rt.Store.Backends().Create(context.Background(), makeBackend("backend-1"))).To(Succeed())
	})

	AfterEach(func() {
		cancel()
	})

	Describe("HttpRouteHandler", func() {
		var (
			handler  *HttpRouteHandler
			virtServ *vs.VirtualServer
			waf      *fakeWaf
		)

		BeforeEach(func() {
			var wafClient *wafclient.Client
			waf, wafClient = startFakeWaf()
			handler = NewHttpRouteHandler(mockJointer, cw, wafClient, rt.Store)
			virtServ = &vs.VirtualServer{UUID: "vs-http", Name: "web", Port: 80, RouteConfig: &vs.VirtualServer_HttpRoutesConfig{
				HttpRoutesConfig: &vs.HttpRoutesConfig{VirtualHosts: []*route.VirtualHost{
					{Domains: []string{"a.local"}, Routes: []*route.Route{{
						Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
						Action: &route.Route_BackendUuid{BackendUuid: "backend-1"},
					}}},
					// virtual host without backend, e.g. only redirects
					{Domains: []string{"b.local"}, Routes: []*route.Route{{
						Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
						Action: &route.Route_Redirect{Redirect: &route.RedirectAction{
							SchemeRewriteSpecifier: &route.RedirectAction_HttpsRedirect{HttpsRedirect: true},
						}},
					}}},
				}},
			}}
		})

		It("should push listener with every virtual hosts and register backend", func() {
			mockJointer.EXPECT().AddListener("backend-1", cw)
			mockJointer.EXPECT().AddEndpointListener("backend-1", cw)

			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1"}))

			listener := &listenerv3.Listener{}
			lds.LastResource(listener)
			vhosts := httpManagerOfListener(listener).GetRouteConfig().GetVirtualHosts()
			Expect(vhosts).To(HaveLen(2))
			Expect(vhosts[0].GetName()).To(Equal("web-0"))
			Expect(vhosts[0].GetDomains()).To(Equal([]string{"a.local"}))
			Expect(vhosts[1].GetName()).To(Equal("web-1"))
			Expect(vhosts[1].GetDomains()).To(Equal([]string{"b.local"}))
		})

		It("should update listener when a virtual host of the virtual server changes", func() {
			mockJointer.EXPECT().AddListener("backend-1", cw).Times(2)
			mockJointer.EXPECT().AddEndpointListener("backend-1", cw).Times(2)
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(HaveLen(1))

			virtServ.GetHttpRoutesConfig().GetVirtualHosts()[0].Domains = []string{"new.local"}
			Expect(handler.UpdateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(HaveLen(2))
			listener := &listenerv3.Listener{}
			lds.LastResource(listener)
			Expect(httpManagerOfListener(listener).GetRouteConfig().GetVirtualHosts()[0].GetDomains()).To(Equal([]string{"new.local"}))
		})

		It("should unregister backend and remove listener on delete", func() {
			mockJointer.EXPECT().AddListener("backend-1", cw)
			mockJointer.EXPECT().AddEndpointListener("backend-1", cw)
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
			// a listener removed before being sent is never sent, wait it reaches envoy first
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1"}))

			mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(nil)
			mockJointer.EXPECT().RemoveListener("backend-1", cw)
			Expect(handler.DeleteRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1", "-tcp-80/vip-1"}))
		})

		It("should remove listener even when backend was already removed", func() {
			mockJointer.EXPECT().AddListener("backend-1", cw)
			mockJointer.EXPECT().AddEndpointListener("backend-1", cw)
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1"}))

			// backend removal was processed before virtual server removal
			mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(oerrors.NewErrNotFound("backend", "backend-1"))
			mockJointer.EXPECT().RemoveListener("backend-1", cw)
			Expect(handler.DeleteRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1", "-tcp-80/vip-1"}))
		})

		It("should clean every routes", func() {
			mockJointer.EXPECT().AddListener("backend-1", cw)
			mockJointer.EXPECT().AddEndpointListener("backend-1", cw)
			Expect(handler.UpdateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())

			mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(nil)
			mockJointer.EXPECT().RemoveListener("backend-1", cw)
			Expect(handler.Clean()).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(ContainElement("-tcp-80/vip-1"))
		})

		It("should rate limit the routes with the rate limit of the virtual server", func() {
			routeOpts, err := anypb.New(&starling_route.VirtualHostRouteOptions{RateLimits: []*starling_route.RouteRateLimit{
				{Actions: []*starling_route.RouteRateLimit_Action{{ActionSpecifier: &starling_route.RouteRateLimit_Action_RemoteAddress_{
					RemoteAddress: &starling_route.RouteRateLimit_Action_RemoteAddress{},
				}}}},
			}})
			Expect(err).ToNot(HaveOccurred())
			virtServ.GetHttpRoutesConfig().GetVirtualHosts()[0].GetRoutes()[0].TypedOptions = routeOpts
			rateLimit, err := anypb.New(&starling_limit.StarlingRateLimit{Descriptors: []*starling_limit.Descriptor{
				{Key: "remote_address", Policy: &starling_limit.Policy{RequestsPerUnit: 10}},
			}})
			Expect(err).ToNot(HaveOccurred())
			virtServ.LimitConfig = []*limit.Config{{Action: &limit.Config_TypedLimit{TypedLimit: rateLimit}}}
			mockJointer.EXPECT().AddListener("backend-1", cw)
			mockJointer.EXPECT().AddEndpointListener("backend-1", cw)

			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1"}))

			listener := &listenerv3.Listener{}
			lds.LastResource(listener)
			httpMan := httpManagerOfListener(listener)
			Expect(httpMan.GetHttpFilters()[0].GetName()).To(Equal("envoy.filters.http.local_ratelimit"))
			envoyRoute := httpMan.GetRouteConfig().GetVirtualHosts()[0].GetRoutes()[0]
			Expect(envoyRoute.GetRoute().GetRateLimits()).To(HaveLen(1))
		})

		Context("with external auth policies", func() {
			BeforeEach(func() {
				Expect(rt.Store.Backends().Create(context.Background(), makeBackend("b-auth"))).To(Succeed())
			})

			withPolicyOn := func(backendUuid string) {
				virtServ.GetHttpRoutesConfig().ExternalAuths = []*route.ExternalAuth{
					{Name: "sso", BackendUuid: backendUuid, Path: "/check"},
				}
			}

			It("uses the backend of a policy like a route backend", func() {
				withPolicyOn("b-auth")
				mockJointer.EXPECT().AddListener("backend-1", cw)
				mockJointer.EXPECT().AddEndpointListener("backend-1", cw)
				mockJointer.EXPECT().AddListener("b-auth", cw)
				mockJointer.EXPECT().AddEndpointListener("b-auth", cw)

				Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
				Eventually(clusters.ResourceNames, 5*time.Second).Should(ContainElements("backend-1", "b-auth"))
			})

			It("fails and registers nothing when the backend of a policy is missing", func() {
				withPolicyOn("b-missing")

				err := handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())
				Expect(err).To(HaveOccurred())
				Consistently(clusters.ResourceNames, 300*time.Millisecond).Should(BeEmpty())
			})

			It("keeps a cluster shared by a route and a removed policy", func() {
				withPolicyOn("backend-1")
				// the route and the policy register it on the first config, the route alone on the second
				mockJointer.EXPECT().AddListener("backend-1", cw).Times(3)
				mockJointer.EXPECT().AddEndpointListener("backend-1", cw).Times(3)
				Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())

				Eventually(clusters.ResourceNames, 5*time.Second).Should(ContainElement("backend-1"))

				// no RemoveListener expected, gomock fails on a release of backend-1
				virtServ.GetHttpRoutesConfig().ExternalAuths = nil
				Expect(handler.UpdateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
				Consistently(clusters.ResourceNames, 300*time.Millisecond).ShouldNot(ContainElement("-backend-1"))
			})

			It("releases the backend of a removed policy", func() {
				withPolicyOn("b-auth")
				mockJointer.EXPECT().AddListener("backend-1", cw).Times(2)
				mockJointer.EXPECT().AddEndpointListener("backend-1", cw).Times(2)
				mockJointer.EXPECT().AddListener("b-auth", cw)
				mockJointer.EXPECT().AddEndpointListener("b-auth", cw)
				Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())

				mockJointer.EXPECT().RemoveEndpointListener("b-auth", cw).Return(nil)
				mockJointer.EXPECT().RemoveListener("b-auth", cw)
				Eventually(clusters.ResourceNames, 5*time.Second).Should(ContainElement("b-auth"))
				virtServ.GetHttpRoutesConfig().ExternalAuths = nil
				Expect(handler.UpdateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
				Eventually(clusters.ResourceNames, 5*time.Second).Should(ContainElement("-b-auth"))
			})
		})

		Context("with a WAF on routes", func() {
			withWaf := func(rte *route.Route, waf *starling_route.Waf) {
				routeOpts, err := anypb.New(&starling_route.VirtualHostRouteOptions{Waf: waf})
				Expect(err).ToNot(HaveOccurred())
				rte.TypedOptions = routeOpts
			}

			BeforeEach(func() {
				vhost := virtServ.GetHttpRoutesConfig().GetVirtualHosts()[0]
				vhost.Routes = []*route.Route{
					{Name: "open", Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/open"}},
						Action: &route.Route_BackendUuid{BackendUuid: "backend-1"}},
					{Name: "default", Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/default"}},
						Action: &route.Route_BackendUuid{BackendUuid: "backend-1"}},
					{Name: "detect", Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/detect"}},
						Action: &route.Route_BackendUuid{BackendUuid: "backend-1"}},
					{Name: "custom", Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
						Action: &route.Route_BackendUuid{BackendUuid: "backend-1"}},
				}
				withWaf(vhost.Routes[1], &starling_route.Waf{})
				withWaf(vhost.Routes[2], &starling_route.Waf{Config: &starling_route.Waf_DetectOnly{DetectOnly: true}})
				withWaf(vhost.Routes[3], &starling_route.Waf{Config: &starling_route.Waf_CustomRules{CustomRules: "SecRuleEngine On"}})
				mockJointer.EXPECT().AddListener("backend-1", cw).AnyTimes()
				mockJointer.EXPECT().AddEndpointListener("backend-1", cw).AnyTimes()
			})

			It("should register the routes with their own config and send their identifier", func() {
				Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())

				configs := waf.Configs()
				Expect(configs).To(HaveLen(2))
				Expect(configs["vs-http-0-detect"].GetDetectOnly()).To(BeTrue())
				Expect(configs["vs-http-0-custom"].GetCustomRules()).To(Equal("SecRuleEngine On"))

				Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-80/vip-1"}))
				listener := &listenerv3.Listener{}
				lds.LastResource(listener)
				httpMan := httpManagerOfListener(listener)
				Expect(httpMan.GetHttpFilters()[0].GetName()).To(Equal("envoy.filters.http.ext_proc"))
				identifiers := make(map[string]string)
				for _, envoyRoute := range httpMan.GetRouteConfig().GetVirtualHosts()[0].GetRoutes() {
					perFilter, hasWaf := envoyRoute.GetTypedPerFilterConfig()["envoy.filters.http.ext_proc"]
					if !hasWaf {
						continue
					}
					perRoute := &ext_procv3.ExtProcPerRoute{}
					Expect(perFilter.UnmarshalTo(perRoute)).To(Succeed())
					identifiers[envoyRoute.GetName()] = ""
					for _, metadata := range perRoute.GetOverrides().GetGrpcInitialMetadata() {
						identifiers[envoyRoute.GetName()] = metadata.GetValue()
					}
				}
				Expect(identifiers).To(Equal(map[string]string{
					"default": "",
					"detect":  "vs-http-0-detect",
					"custom":  "vs-http-0-custom",
				}))
			})

			It("should unregister the configs a route drops on update", func() {
				Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
				Expect(waf.Configs()).To(HaveKey("vs-http-0-detect"))

				detectRoute := virtServ.GetHttpRoutesConfig().GetVirtualHosts()[0].GetRoutes()[2]
				detectRoute.TypedOptions = nil
				Expect(handler.UpdateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
				Expect(waf.Configs()).To(HaveLen(1))
				Expect(waf.Configs()).To(HaveKey("vs-http-0-custom"))
			})

			It("should keep apart the routes of the same name in two virtual hosts", func() {
				otherHost := virtServ.GetHttpRoutesConfig().GetVirtualHosts()[1]
				otherHost.Routes = []*route.Route{{
					Name:   "custom",
					Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
					Action: &route.Route_BackendUuid{BackendUuid: "backend-1"},
				}}
				withWaf(otherHost.Routes[0], &starling_route.Waf{Config: &starling_route.Waf_CustomRules{CustomRules: "SecRuleEngine Off"}})

				Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
				configs := waf.Configs()
				Expect(configs["vs-http-0-custom"].GetCustomRules()).To(Equal("SecRuleEngine On"))
				Expect(configs["vs-http-1-custom"].GetCustomRules()).To(Equal("SecRuleEngine Off"))
			})

			It("should unregister the configs on delete", func() {
				Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
				Expect(waf.Configs()).To(HaveLen(2))
				mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(nil).AnyTimes()
				mockJointer.EXPECT().RemoveListener("backend-1", cw).AnyTimes()

				Expect(handler.DeleteRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
				Expect(waf.Configs()).To(BeEmpty())
			})

			It("should leave the configs to the WAF on clean, it stops with the agent", func() {
				Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
				mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(nil).AnyTimes()
				mockJointer.EXPECT().RemoveListener("backend-1", cw).AnyTimes()

				Expect(handler.Clean()).To(Succeed())
				Expect(waf.Configs()).To(HaveLen(2))
				Eventually(lds.ResourceNames, 5*time.Second).Should(BeEmpty())
			})
		})

		It("should refuse a route whose options are not the starling ones", func() {
			wrongOpts, err := anypb.New(wrapperspb.String("not route options"))
			Expect(err).ToNot(HaveOccurred())
			virtServ.GetHttpRoutesConfig().GetVirtualHosts()[0].GetRoutes()[0].TypedOptions = wrongOpts

			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).
				To(MatchError(ContainSubstring("failed to retrieve agent options of route")))
		})

		It("should release a backend the virtual server no longer routes to", func() {
			Expect(rt.Store.Backends().Create(context.Background(), makeBackend("backend-2"))).To(Succeed())
			mockJointer.EXPECT().AddListener("backend-1", cw)
			mockJointer.EXPECT().AddEndpointListener("backend-1", cw)
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())

			mockJointer.EXPECT().AddListener("backend-2", cw)
			mockJointer.EXPECT().AddEndpointListener("backend-2", cw)
			mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(nil)
			mockJointer.EXPECT().RemoveListener("backend-1", cw)
			virtServ.GetHttpRoutesConfig().GetVirtualHosts()[0].GetRoutes()[0].Action = &route.Route_BackendUuid{BackendUuid: "backend-2"}
			Expect(handler.UpdateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
		})

		It("should keep following a backend another virtual server still routes to", func() {
			mockJointer.EXPECT().AddListener("backend-1", cw).Times(2)
			mockJointer.EXPECT().AddEndpointListener("backend-1", cw).Times(2)
			otherVs := proto.Clone(virtServ).(*vs.VirtualServer)
			otherVs.UUID = "vs-other"
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())
			Expect(handler.CreateRoute(ctx, aVip, otherVs, otherVs.GetHttpRoutesConfig())).To(Succeed())

			// no RemoveEndpointListener expected yet
			Expect(handler.DeleteRoute(ctx, aVip, virtServ, virtServ.GetHttpRoutesConfig())).To(Succeed())

			mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(nil)
			mockJointer.EXPECT().RemoveListener("backend-1", cw)
			Expect(handler.DeleteRoute(ctx, aVip, otherVs, otherVs.GetHttpRoutesConfig())).To(Succeed())
		})
	})

	Describe("TlsTcpRouteHandler", func() {
		var (
			handler  *TlsTcpRouteHandler
			virtServ *vs.VirtualServer
		)

		BeforeEach(func() {
			handler = NewTlsTcpRouteHandler(mockJointer, cw, rt.Store)
			virtServ = &vs.VirtualServer{UUID: "vs-tls", Port: 443, RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{
				TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: []*vs.TlsTcpRouteConfig{
					{BackendUuid: "backend-1", Mode: &vs.TlsTcpRouteConfig_Tls{Tls: &core.TLS{ServerNames: []string{"a.local"}, CertificatesUuid: []string{"cert-1"}}}},
				}},
			}}
		})

		It("should push a tls listener proxying to backend", func() {
			mockJointer.EXPECT().AddListener("backend-1", cw)
			mockJointer.EXPECT().AddEndpointListener("backend-1", cw)
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTlsTcpRoutesConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-443/vip-1"}))

			listener := &listenerv3.Listener{}
			lds.LastResource(listener)
			chain := listener.GetFilterChains()[0]
			Expect(chain.GetFilterChainMatch().GetServerNames()).To(Equal([]string{"a.local"}))
			tcpProxy := &tcp_proxyv3.TcpProxy{}
			Expect(chain.GetFilters()[0].GetTypedConfig().UnmarshalTo(tcpProxy)).To(Succeed())
			Expect(tcpProxy.GetCluster()).To(Equal("backend-1"))
		})

		It("should remove listener even when backend was already removed", func() {
			mockJointer.EXPECT().AddListener("backend-1", cw)
			mockJointer.EXPECT().AddEndpointListener("backend-1", cw)
			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTlsTcpRoutesConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-443/vip-1"}))

			mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(oerrors.NewErrNotFound("backend", "backend-1"))
			mockJointer.EXPECT().RemoveListener("backend-1", cw)
			Expect(handler.DeleteRoute(ctx, aVip, virtServ, virtServ.GetTlsTcpRoutesConfig())).To(Succeed())
			Eventually(lds.ResourceNames, 5*time.Second).Should(Equal([]string{"tcp-443/vip-1", "-tcp-443/vip-1"}))
		})

		It("should remove listener on delete and clean", func() {
			mockJointer.EXPECT().AddListener("backend-1", cw).Times(2)
			mockJointer.EXPECT().AddEndpointListener("backend-1", cw).Times(2)
			mockJointer.EXPECT().RemoveEndpointListener("backend-1", cw).Return(nil).Times(2)
			mockJointer.EXPECT().RemoveListener("backend-1", cw).Times(2)

			Expect(handler.CreateRoute(ctx, aVip, virtServ, virtServ.GetTlsTcpRoutesConfig())).To(Succeed())
			Expect(handler.DeleteRoute(ctx, aVip, virtServ, virtServ.GetTlsTcpRoutesConfig())).To(Succeed())

			Expect(handler.UpdateRoute(ctx, aVip, virtServ, virtServ.GetTlsTcpRoutesConfig())).To(Succeed())
			Expect(handler.Clean()).To(Succeed())
			Eventually(func() int {
				removals := 0
				for _, name := range lds.ResourceNames() {
					if name == "-tcp-443/vip-1" {
						removals++
					}
				}
				return removals
			}, 10*time.Second).Should(Equal(2))
		})
	})
})
