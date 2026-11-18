package envoyctl

import (
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	http_connection_managerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	tcp_proxyv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/tcp_proxy/v3"
	udp_proxyv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/udp/udp_proxy/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func httpManagerOfChain(chain *listenerv3.FilterChain) *http_connection_managerv3.HttpConnectionManager { //nolint:unparam // called for its assertions
	GinkgoHelper()
	Expect(chain.GetFilters()).To(HaveLen(1))
	Expect(chain.GetFilters()[0].GetName()).To(Equal("envoy.filters.network.http_connection_manager"))
	return unmarshalAny(chain.GetFilters()[0].GetTypedConfig(), &http_connection_managerv3.HttpConnectionManager{})
}

var _ = Describe("Listener conversion", func() {
	var (
		aVip     *vip.VIP
		virtServ *vs.VirtualServer
	)

	BeforeEach(func() {
		aVip = &vip.VIP{UUID: "vip-uuid", Ip: "10.0.0.1"}
		virtServ = &vs.VirtualServer{UUID: "vs-uuid", Port: 443}
	})

	Context("makeEnvoyListener", func() {
		It("should create a valid tcp listener", func() {
			listener := makeEnvoyListener("vs-uuid/vip", aVip, virtServ, tcpFilterChain("vs-uuid", "backend-uuid"), false, makeTlsInspectorListFilt())
			expectValidEnvoyConfig(listener)

			sockAddr := listener.GetAddress().GetSocketAddress()
			Expect(sockAddr.GetAddress()).To(Equal("10.0.0.1"))
			Expect(sockAddr.GetPortValue()).To(Equal(uint32(443)))
			Expect(sockAddr.GetProtocol()).To(Equal(corev3.SocketAddress_TCP))
			Expect(listener.GetUdpListenerConfig()).To(BeNil())
			Expect(listener.GetEnableReusePort().GetValue()).To(BeTrue())
			Expect(listener.GetStatPrefix()).To(Equal("vs-uuid/vip"))
			Expect(listener.GetListenerFilters()).To(HaveLen(1))
			Expect(listener.GetListenerFilters()[0].GetName()).To(Equal("envoy.filters.listener.tls_inspector"))
		})

		It("should create a valid udp listener", func() {
			listener := makeEnvoyListener("vs-uuid/vip", aVip, virtServ, nil, true, udpProxyListenerFilter("vs-uuid", "backend-uuid"))
			expectValidEnvoyConfig(listener)
			Expect(listener.GetAddress().GetSocketAddress().GetProtocol()).To(Equal(corev3.SocketAddress_UDP))
			Expect(listener.GetUdpListenerConfig().GetDownstreamSocketConfig().GetMaxRxDatagramSize().GetValue()).To(Equal(uint64(rxDatagramSizeUdp)))
			// envoy rejects a connection-less udp listener with filter chains
			Expect(listener.GetFilterChains()).To(BeEmpty())
			Expect(listener.GetListenerFilters()).To(HaveLen(1))
			Expect(listener.GetListenerFilters()[0].GetName()).To(Equal("envoy.filters.udp_listener.udp_proxy"))
		})

		It("should accept original src and dst listener filters", func() {
			listener := makeEnvoyListener("name", aVip, virtServ, tcpFilterChain("vs-uuid", "backend-uuid"), false,
				makeOriginSrcListFilt(), makeOriginDstListFilt())
			expectValidEnvoyConfig(listener)
			Expect(listener.GetListenerFilters()[0].GetName()).To(Equal("envoy.filters.listener.original_src"))
			Expect(listener.GetListenerFilters()[1].GetName()).To(Equal("envoy.filters.listener.original_dst"))
		})
	})

	Context("tcpFilterChain", func() {
		It("should proxy to the backend", func() {
			chains := tcpFilterChain("vs-uuid", "backend-uuid")
			Expect(chains).To(HaveLen(1))
			Expect(chains[0].GetFilters()[0].GetName()).To(Equal("envoy.filters.network.tcp_proxy"))
			tcpProxy := unmarshalAny(chains[0].GetFilters()[0].GetTypedConfig(), &tcp_proxyv3.TcpProxy{})
			Expect(tcpProxy.GetCluster()).To(Equal("backend-uuid"))
			Expect(tcpProxy.GetStatPrefix()).To(Equal("vs-uuid"))
			Expect(tcpProxy.GetAccessLog()).To(HaveLen(1))
		})
	})

	Context("udpProxyListenerFilter", func() {
		It("should route every datagram to the backend", func() {
			filter := udpProxyListenerFilter("vs-uuid", "backend-uuid")
			udpProxy := unmarshalAny(filter.GetTypedConfig(), &udp_proxyv3.UdpProxyConfig{})
			Expect(udpProxy.GetStatPrefix()).To(Equal("vs-uuid"))
			action := udpProxy.GetMatcher().GetOnNoMatch().GetAction()
			Expect(action.GetName()).To(Equal("route"))
			Expect(unmarshalAny(action.GetTypedConfig(), &udp_proxyv3.Route{}).GetCluster()).To(Equal("backend-uuid"))
		})
	})

	Context("tlsTcpFilterChain", func() {
		It("should create one tls terminated chain per route matching on sni", func() {
			chains := tlsTcpFilterChain("vs-uuid", []*vs.TlsTcpRouteConfig{
				{BackendUuid: "backend-1", Mode: &vs.TlsTcpRouteConfig_Tls{Tls: &core.TLS{ServerNames: []string{"a.local"}, CertificatesUuid: []string{"cert-1"}, AlpnProtocols: []string{"h2"}}}},
				{BackendUuid: "backend-2", Mode: &vs.TlsTcpRouteConfig_Tls{Tls: &core.TLS{ServerNames: []string{"b.local"}, CertificatesUuid: []string{"cert-2"}}}},
			})
			Expect(chains).To(HaveLen(2))
			for _, chain := range chains {
				expectValidEnvoyConfig(chain)
			}
			Expect(chains[0].GetFilterChainMatch().GetServerNames()).To(Equal([]string{"a.local"}))
			tlsCtx := unmarshalAny(chains[0].GetTransportSocket().GetTypedConfig(), &tlsv3.DownstreamTlsContext{})
			Expect(tlsCtx.GetCommonTlsContext().GetAlpnProtocols()).To(Equal([]string{"h2"}))
			Expect(tlsCtx.GetCommonTlsContext().GetTlsParams().GetTlsMinimumProtocolVersion()).To(Equal(tlsv3.TlsParameters_TLSv1_2))
			sds := tlsCtx.GetCommonTlsContext().GetTlsCertificateSdsSecretConfigs()
			Expect(sds).To(HaveLen(1))
			Expect(sds[0].GetName()).To(Equal("cert-1"))
			Expect(sds[0].GetSdsConfig().GetAds()).ToNot(BeNil())

			tcpProxy := unmarshalAny(chains[1].GetFilters()[0].GetTypedConfig(), &tcp_proxyv3.TcpProxy{})
			Expect(tcpProxy.GetCluster()).To(Equal("backend-2"))
		})

		It("should not terminate tls on a passthrough route", func() {
			chains := tlsTcpFilterChain("vs-uuid", []*vs.TlsTcpRouteConfig{
				{BackendUuid: "backend-1", Mode: &vs.TlsTcpRouteConfig_Tls{Tls: &core.TLS{ServerNames: []string{"a.local"}, CertificatesUuid: []string{"cert-1"}}}},
				{BackendUuid: "backend-2", Mode: &vs.TlsTcpRouteConfig_Passthrough{Passthrough: &vs.TlsPassthrough{ServerNames: []string{"b.local", "*.c.local"}}}},
				{BackendUuid: "backend-3", Mode: &vs.TlsTcpRouteConfig_Passthrough{Passthrough: &vs.TlsPassthrough{}}},
			})
			Expect(chains).To(HaveLen(3))
			for _, chain := range chains {
				expectValidEnvoyConfig(chain)
			}
			Expect(chains[0].GetTransportSocket()).ToNot(BeNil())

			Expect(chains[1].GetFilterChainMatch().GetServerNames()).To(Equal([]string{"b.local", "*.c.local"}))
			Expect(chains[1].GetTransportSocket()).To(BeNil())
			tcpProxy := unmarshalAny(chains[1].GetFilters()[0].GetTypedConfig(), &tcp_proxyv3.TcpProxy{})
			Expect(tcpProxy.GetCluster()).To(Equal("backend-2"))

			By("matching every connection left when it has no server names")
			Expect(chains[2].GetFilterChainMatch().GetServerNames()).To(BeEmpty())
			Expect(chains[2].GetTransportSocket()).To(BeNil())
		})
	})

	Context("http", func() {
		var (
			opts   *starling_vsopts.HttpOptions
			routes []*HttpRoute
		)

		BeforeEach(func() {
			opts = &starling_vsopts.HttpOptions{}
			routes = []*HttpRoute{
				{
					Name: "vs-0",
					VirtualHost: &route.VirtualHost{Domains: []string{"a.local"}, Routes: []*route.Route{{
						Name:   "default",
						Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
						Action: &route.Route_BackendUuid{BackendUuid: "backend-1"},
					}}},
					Backends: []*backend.Backend{{UUID: "backend-1"}},
				},
			}
		})

		Context("virtualHostToFilterChain", func() {
			It("should create a single plain http chain when no tls options", func() {
				chains := virtualHostToFilterChain("vs-uuid", nil, routes, opts, nil)
				Expect(chains).To(HaveLen(1))
				expectValidEnvoyConfig(chains[0])
				Expect(chains[0].GetTransportSocket()).To(BeNil())
				httpManagerOfChain(chains[0])
			})

			It("should create one tls chain per tls option", func() {
				routes[0].VirtualHost.TlsOptions = []*core.TLS{
					{ServerNames: []string{"a.local"}, CertificatesUuid: []string{"cert-a"}},
					{ServerNames: []string{"b.local"}, CertificatesUuid: []string{"cert-b"}},
				}
				chains := virtualHostToFilterChain("vs-uuid", nil, routes, opts, nil)
				Expect(chains).To(HaveLen(2))
				for _, chain := range chains {
					expectValidEnvoyConfig(chain)
					Expect(chain.GetTransportSocket().GetName()).To(Equal("envoy.transport_sockets.tls"))
					httpManagerOfChain(chain)
				}
				Expect(chains[1].GetFilterChainMatch().GetServerNames()).To(Equal([]string{"b.local"}))
			})
		})

		Context("virtualHostToHttpManager", func() {
			It("should apply edge proxy defaults", func() {
				httpMan := virtualHostToHttpManager("vs-uuid", nil, routes, opts, nil)
				expectValidEnvoyConfig(httpMan)

				Expect(httpMan.GetCodecType()).To(Equal(http_connection_managerv3.HttpConnectionManager_AUTO))
				Expect(httpMan.GetUseRemoteAddress().GetValue()).To(BeTrue())
				Expect(httpMan.GetNormalizePath().GetValue()).To(BeTrue())
				Expect(httpMan.GetMergeSlashes()).To(BeTrue())
				Expect(httpMan.GetStripAnyHostPort()).To(BeTrue())
				Expect(httpMan.GetUpgradeConfigs()).To(HaveLen(1))
				Expect(httpMan.GetUpgradeConfigs()[0].GetUpgradeType()).To(Equal("websocket"))
				Expect(httpMan.GetCommonHttpProtocolOptions().GetIdleTimeout().AsDuration()).To(Equal(time.Hour))
				Expect(httpMan.GetCommonHttpProtocolOptions().GetHeadersWithUnderscoresAction()).To(Equal(corev3.HttpProtocolOptions_REJECT_REQUEST))
				Expect(httpMan.GetStreamIdleTimeout().AsDuration()).To(Equal(5 * time.Minute))
				Expect(httpMan.GetRequestTimeout().AsDuration()).To(BeZero())
				Expect(httpMan.GetHttp2ProtocolOptions().GetMaxConcurrentStreams().GetValue()).To(Equal(uint32(1000000)))
				Expect(httpMan.GetHttpFilters()).To(HaveLen(1))
				Expect(httpMan.GetHttpFilters()[0].GetName()).To(Equal("envoy.filters.http.router"))

				vhosts := httpMan.GetRouteConfig().GetVirtualHosts()
				Expect(vhosts).To(HaveLen(1))
				Expect(vhosts[0].GetName()).To(Equal("vs-0"))
				Expect(vhosts[0].GetDomains()).To(Equal([]string{"a.local"}))
				Expect(vhosts[0].GetRoutes()).To(HaveLen(1))
				defaultRoute := vhosts[0].GetRoutes()[0]
				Expect(defaultRoute.GetMatch().GetPrefix()).To(Equal("/"))
				Expect(defaultRoute.GetRoute().GetCluster()).To(Equal("backend-1"))
				Expect(defaultRoute.GetRoute().GetIdleTimeout().AsDuration()).To(Equal(15 * time.Second))
			})

			DescribeTable("should convert codec type",
				func(codec starling_vsopts.HttpOptions_CodecType, expected http_connection_managerv3.HttpConnectionManager_CodecType) {
					opts.CodecType = codec
					Expect(virtualHostToHttpManager("vs-uuid", nil, routes, opts, nil).GetCodecType()).To(Equal(expected))
				},
				Entry("auto", starling_vsopts.HttpOptions_AUTO, http_connection_managerv3.HttpConnectionManager_AUTO),
				Entry("http1", starling_vsopts.HttpOptions_HTTP1, http_connection_managerv3.HttpConnectionManager_HTTP1),
				Entry("http2", starling_vsopts.HttpOptions_HTTP2, http_connection_managerv3.HttpConnectionManager_HTTP2),
			)

			It("should apply user options", func() {
				opts.StreamIdleTimeout = durationpb.New(time.Minute)
				opts.RequestTimeout = durationpb.New(30 * time.Second)
				opts.RequestHeadersToAdd = []*core.HeaderValueOption{{Header: &core.HeaderValue{Key: "X-Req", ValueSource: &core.HeaderValue_Value{Value: "1"}}, Append: true}}
				opts.RequestHeadersToRemove = []string{"X-Remove-Req"}
				opts.ResponseHeadersToAdd = []*core.HeaderValueOption{{Header: &core.HeaderValue{Key: "X-Resp", ValueSource: &core.HeaderValue_Value{Value: "2"}}}}
				opts.ResponseHeadersToRemove = []string{"X-Remove-Resp"}
				routes[0].VirtualHost.PathPrefix = "/api"

				httpMan := virtualHostToHttpManager("vs-uuid", nil, routes, opts, nil)
				expectValidEnvoyConfig(httpMan)

				Expect(httpMan.GetStreamIdleTimeout().AsDuration()).To(Equal(time.Minute))
				Expect(httpMan.GetRequestTimeout().AsDuration()).To(Equal(30 * time.Second))
				routeConf := httpMan.GetRouteConfig()
				Expect(routeConf.GetRequestHeadersToAdd()).To(HaveLen(1))
				Expect(routeConf.GetRequestHeadersToAdd()[0].GetHeader().GetKey()).To(Equal("X-Req"))
				Expect(routeConf.GetRequestHeadersToAdd()[0].GetAppendAction()).To(Equal(corev3.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD))
				Expect(routeConf.GetResponseHeadersToAdd()[0].GetAppendAction()).To(Equal(corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD))
				Expect(routeConf.GetRequestHeadersToRemove()).To(Equal([]string{"X-Remove-Req"}))
				Expect(routeConf.GetResponseHeadersToRemove()).To(Equal([]string{"X-Remove-Resp"}))

				defaultRoute := routeConf.GetVirtualHosts()[0].GetRoutes()[0]
				Expect(defaultRoute.GetMatch().GetPathSeparatedPrefix()).To(Equal("/api"))
				Expect(defaultRoute.GetRoute().GetTimeout().AsDuration()).To(Equal(30 * time.Second))
				Expect(defaultRoute.GetRoute().GetIdleTimeout().AsDuration()).To(Equal(time.Minute))
			})

			DescribeTable("should join the path prefix of the virtual host with the prefix of a route",
				func(pathPrefix, routePrefix string, expected *routev3.RouteMatch) {
					routes[0].VirtualHost.PathPrefix = pathPrefix
					routes[0].VirtualHost.Routes = []*route.Route{{
						Name:   "route",
						Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: routePrefix}},
						Action: &route.Route_BackendUuid{BackendUuid: "backend-1"},
					}}
					httpMan := virtualHostToHttpManager("vs-uuid", nil, routes, opts, nil)
					expectValidEnvoyConfig(httpMan)
					match := httpMan.GetRouteConfig().GetVirtualHosts()[0].GetRoutes()[0].GetMatch()
					Expect(match.GetPathSpecifier()).To(Equal(expected.GetPathSpecifier()))
				},
				Entry("route on the prefix stops at a path segment", "/api", "/",
					&routev3.RouteMatch{PathSpecifier: &routev3.RouteMatch_PathSeparatedPrefix{PathSeparatedPrefix: "/api"}}),
				Entry("trailing slash of the prefix is dropped", "/api/", "/",
					&routev3.RouteMatch{PathSpecifier: &routev3.RouteMatch_PathSeparatedPrefix{PathSeparatedPrefix: "/api"}}),
				Entry("route under the prefix keeps a plain prefix", "/api", "/users",
					&routev3.RouteMatch{PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/api/users"}}),
				Entry("no prefix keeps the route", "", "/",
					&routev3.RouteMatch{PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"}}),
				Entry("root prefix keeps the route", "/", "/",
					&routev3.RouteMatch{PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"}}),
			)

			It("should prefix exact paths but not regexes with the path prefix of the virtual host", func() {
				routes[0].VirtualHost.PathPrefix = "/api"
				routes[0].VirtualHost.Routes = []*route.Route{
					{
						Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Path{Path: "/health"}},
						Action: &route.Route_BackendUuid{BackendUuid: "backend-1"},
					},
					{
						Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_SafeRegex{SafeRegex: "^/v[0-9]+/.*"}},
						Action: &route.Route_BackendUuid{BackendUuid: "backend-1"},
					},
				}
				httpMan := virtualHostToHttpManager("vs-uuid", nil, routes, opts, nil)
				expectValidEnvoyConfig(httpMan)
				envoyRoutes := httpMan.GetRouteConfig().GetVirtualHosts()[0].GetRoutes()
				Expect(envoyRoutes[0].GetMatch().GetPath()).To(Equal("/api/health"))
				Expect(envoyRoutes[1].GetMatch().GetSafeRegex().GetRegex()).To(Equal("^/v[0-9]+/.*"))
			})

			It("should only convert the routes of the virtual host", func() {
				routes[0].Backends = nil
				routes[0].VirtualHost.Routes = []*route.Route{
					{
						Name:  "redirect",
						Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
						Action: &route.Route_Redirect{Redirect: &route.RedirectAction{
							SchemeRewriteSpecifier: &route.RedirectAction_HttpsRedirect{HttpsRedirect: true},
						}},
					},
				}
				httpMan := virtualHostToHttpManager("vs-uuid", nil, routes, opts, nil)
				expectValidEnvoyConfig(httpMan)
				envoyRoutes := httpMan.GetRouteConfig().GetVirtualHosts()[0].GetRoutes()
				Expect(envoyRoutes).To(HaveLen(1))
				Expect(envoyRoutes[0].GetRedirect().GetHttpsRedirect()).To(BeTrue())
			})
		})

		Context("virtualHostRouteToEnvoyRoute", func() {
			DescribeTable("should convert path matcher",
				func(match *route.RouteMatch, check func(*routev3.RouteMatch)) {
					envoyRoute := virtualHostRouteToEnvoyRoute(&route.Route{
						Name:  "rte",
						Match: match,
						Action: &route.Route_DirectResponse{DirectResponse: &route.DirectResponseAction{
							Status: 200,
						}},
					}, "", nil)
					expectValidEnvoyConfig(envoyRoute)
					check(envoyRoute.GetMatch())
				},
				Entry("prefix", &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/api"}}, func(m *routev3.RouteMatch) {
					Expect(m.GetPrefix()).To(Equal("/api"))
				}),
				Entry("path", &route.RouteMatch{PathSpecifier: &route.RouteMatch_Path{Path: "/exact"}}, func(m *routev3.RouteMatch) {
					Expect(m.GetPath()).To(Equal("/exact"))
				}),
				Entry("regex", &route.RouteMatch{PathSpecifier: &route.RouteMatch_SafeRegex{SafeRegex: "^/v[0-9]+"}, CaseSensitive: wrapperspb.Bool(false)}, func(m *routev3.RouteMatch) {
					Expect(m.GetSafeRegex().GetRegex()).To(Equal("^/v[0-9]+"))
					Expect(m.GetCaseSensitive().GetValue()).To(BeFalse())
				}),
			)

			It("should convert redirect action", func() {
				envoyRoute := virtualHostRouteToEnvoyRoute(&route.Route{
					Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
					Action: &route.Route_Redirect{Redirect: &route.RedirectAction{
						HostRedirect:           "other.local",
						PortRedirect:           8443,
						PathRedirect:           "/new",
						SchemeRewriteSpecifier: &route.RedirectAction_SchemeRedirect{SchemeRedirect: "https"},
					}},
				}, "", nil)
				expectValidEnvoyConfig(envoyRoute)
				redirect := envoyRoute.GetRedirect()
				Expect(redirect.GetHostRedirect()).To(Equal("other.local"))
				Expect(redirect.GetPortRedirect()).To(Equal(uint32(8443)))
				Expect(redirect.GetPathRedirect()).To(Equal("/new"))
				Expect(redirect.GetSchemeRedirect()).To(Equal("https"))
			})

			It("should convert direct response with inline string", func() {
				envoyRoute := virtualHostRouteToEnvoyRoute(&route.Route{
					Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
					Action: &route.Route_DirectResponse{DirectResponse: &route.DirectResponseAction{
						Status:    503,
						Specifier: &route.DirectResponseAction_InlineString{InlineString: "maintenance"},
					}},
				}, "", nil)
				expectValidEnvoyConfig(envoyRoute)
				Expect(envoyRoute.GetDirectResponse().GetStatus()).To(Equal(uint32(503)))
				Expect(envoyRoute.GetDirectResponse().GetBody().GetInlineString()).To(Equal("maintenance"))
			})

			It("should convert backend action with the timeouts of the options", func() {
				envoyRoute := virtualHostRouteToEnvoyRoute(&route.Route{
					Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
					Action: &route.Route_BackendUuid{BackendUuid: "backend-1"},
				}, "/app/", &starling_vsopts.HttpOptions{RequestTimeout: durationpb.New(10 * time.Second)})
				expectValidEnvoyConfig(envoyRoute)
				Expect(envoyRoute.GetMatch().GetPathSeparatedPrefix()).To(Equal("/app"))
				Expect(envoyRoute.GetRoute().GetCluster()).To(Equal("backend-1"))
				Expect(envoyRoute.GetRoute().GetTimeout().AsDuration()).To(Equal(10 * time.Second))
				Expect(envoyRoute.GetRoute().GetIdleTimeout().AsDuration()).To(Equal(15 * time.Second))
			})

			It("should convert direct response with inline bytes", func() {
				envoyRoute := virtualHostRouteToEnvoyRoute(&route.Route{
					Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
					Action: &route.Route_DirectResponse{DirectResponse: &route.DirectResponseAction{
						Status:    200,
						Specifier: &route.DirectResponseAction_InlineBytes{InlineBytes: []byte("ok")},
					}},
				}, "", nil)
				expectValidEnvoyConfig(envoyRoute)
				Expect(envoyRoute.GetDirectResponse().GetBody().GetInlineBytes()).To(Equal([]byte("ok")))
			})
		})
	})
})

var _ = Describe("Load assignment conversion", func() {
	It("should create endpoints with weight only when set", func() {
		cla := makeEnvoyClusterLoadAssignment("backend-uuid", []*core.IPAddr{
			{Ip: "10.0.0.1", Port: 80},
			{Ip: "10.0.0.2", Port: 8080, LoadBalancingWeight: 5},
		})
		expectValidEnvoyConfig(cla)
		Expect(cla.GetClusterName()).To(Equal("backend-uuid"))
		lbEndpoints := cla.GetEndpoints()[0].GetLbEndpoints()
		Expect(lbEndpoints).To(HaveLen(2))
		Expect(lbEndpoints[0].GetEndpoint().GetAddress().GetSocketAddress().GetAddress()).To(Equal("10.0.0.1"))
		Expect(lbEndpoints[0].GetLoadBalancingWeight()).To(BeNil())
		Expect(lbEndpoints[1].GetEndpoint().GetAddress().GetSocketAddress().GetPortValue()).To(Equal(uint32(8080)))
		Expect(lbEndpoints[1].GetLoadBalancingWeight().GetValue()).To(Equal(uint32(5)))
	})

	It("should create an empty assignment without endpoints", func() {
		cla := makeEnvoyClusterLoadAssignment("backend-uuid", nil)
		expectValidEnvoyConfig(cla)
		Expect(cla.GetEndpoints()[0].GetLbEndpoints()).To(BeEmpty())
	})
})
