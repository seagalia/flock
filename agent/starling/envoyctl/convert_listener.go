package envoyctl

import (
	"strings"

	xds_corev3 "github.com/cncf/xds/go/xds/core/v3"
	xds_matcher "github.com/cncf/xds/go/xds/type/matcher/v3"
	accesslogv3 "github.com/envoyproxy/go-control-plane/envoy/config/accesslog/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	routerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/router/v3"
	original_dstv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/listener/original_dst/v3"
	original_srcv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/listener/original_src/v3"
	proxy_protocolv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/listener/proxy_protocol/v3"
	tls_inspectorv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/listener/tls_inspector/v3"
	http_connection_managerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	tcp_proxyv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/tcp_proxy/v3"
	udp_proxyv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/udp/udp_proxy/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	rxDatagramSizeUdp = 9000
)

func makeEnvoyListener(name string, aVip *vip.VIP, virtServ *vs.VirtualServer, filterChains []*listenerv3.FilterChain, isUdp bool, listenersFilters ...*listenerv3.ListenerFilter) *listenerv3.Listener {
	listFilters := make([]*listenerv3.ListenerFilter, 0)
	if len(listenersFilters) > 0 {
		listFilters = append(listFilters, listenersFilters...)
	}
	var udpListenerConfig *listenerv3.UdpListenerConfig
	prot := corev3.SocketAddress_TCP
	if isUdp {
		prot = corev3.SocketAddress_UDP
		udpListenerConfig = &listenerv3.UdpListenerConfig{
			DownstreamSocketConfig: &corev3.UdpSocketConfig{
				MaxRxDatagramSize: &wrapperspb.UInt64Value{Value: rxDatagramSizeUdp},
			},
		}
	}
	return &listenerv3.Listener{
		Name: name,
		Address: &corev3.Address{
			Address: &corev3.Address_SocketAddress{
				SocketAddress: &corev3.SocketAddress{
					Protocol: prot,
					Address:  aVip.GetIp(),
					PortSpecifier: &corev3.SocketAddress_PortValue{
						PortValue: virtServ.GetPort(),
					},
				},
			},
		},
		UdpListenerConfig: udpListenerConfig,
		EnableReusePort:   &wrapperspb.BoolValue{Value: true},
		StatPrefix:        name,
		FilterChains:      filterChains,
		PerConnectionBufferLimitBytes: &wrapperspb.UInt32Value{
			Value: 32768, // 32 KiB as suggested for envoy edge proxy
		},
		ListenerFilters: listFilters,
	}
}

func tlsTcpFilterChain(vsUuid string, routes []*vs.TlsTcpRouteConfig) []*listenerv3.FilterChain {
	accessLogConf, _ := anypb.New(accessLogStdout) //nolint
	chains := make([]*listenerv3.FilterChain, 0)
	for _, rte := range routes {
		connMan, _ := anypb.New(&tcp_proxyv3.TcpProxy{
			StatPrefix: vsUuid,
			ClusterSpecifier: &tcp_proxyv3.TcpProxy_Cluster{
				Cluster: rte.GetBackendUuid(),
			},
			AccessLog: []*accesslogv3.AccessLog{
				{
					Name:       "envoy.access_loggers.stdout",
					ConfigType: &accesslogv3.AccessLog_TypedConfig{TypedConfig: accessLogConf},
				},
			},
		})
		chain := &listenerv3.FilterChain{
			Filters: []*listenerv3.Filter{
				{
					Name: "envoy.filters.network.tcp_proxy",
					ConfigType: &listenerv3.Filter_TypedConfig{
						TypedConfig: connMan,
					},
				},
			},
		}
		// without a transport socket envoy only reads the SNI (tls_inspector) and proxies the encrypted bytes
		if passthrough := rte.GetPassthrough(); passthrough != nil {
			chain.FilterChainMatch = &listenerv3.FilterChainMatch{ServerNames: passthrough.GetServerNames()}
			chains = append(chains, chain)
			continue
		}
		tlsOpt := rte.GetTls()
		chain.FilterChainMatch = &listenerv3.FilterChainMatch{ServerNames: tlsOpt.GetServerNames()}
		chain.TransportSocket = tlsTerminationSocket(tlsOpt)
		chains = append(chains, chain)
	}
	return chains
}

func tlsTerminationSocket(tlsOpt *core.TLS) *corev3.TransportSocket {
	tlsContext, _ := anypb.New(&tlsv3.DownstreamTlsContext{
		CommonTlsContext: &tlsv3.CommonTlsContext{
			AlpnProtocols:                  tlsOpt.GetAlpnProtocols(),
			TlsCertificateSdsSecretConfigs: certificateUuidsToSds(tlsOpt.GetCertificatesUuid()),
			TlsParams: &tlsv3.TlsParameters{
				// already envoy's default, explicit to not depend on it
				TlsMinimumProtocolVersion: tlsv3.TlsParameters_TLSv1_2,
			},
		},
	})
	return &corev3.TransportSocket{
		Name: "envoy.transport_sockets.tls",
		ConfigType: &corev3.TransportSocket_TypedConfig{
			TypedConfig: tlsContext,
		},
	}
}

func tcpFilterChain(vsUuid string, backendUuid string) []*listenerv3.FilterChain {
	accessLogConf, _ := anypb.New(accessLogStdout) //nolint
	chains := make([]*listenerv3.FilterChain, 0, 1)
	connMan, _ := anypb.New(&tcp_proxyv3.TcpProxy{
		StatPrefix: vsUuid,
		ClusterSpecifier: &tcp_proxyv3.TcpProxy_Cluster{
			Cluster: backendUuid,
		},
		AccessLog: []*accesslogv3.AccessLog{
			{
				Name:       "envoy.access_loggers.stdout",
				ConfigType: &accesslogv3.AccessLog_TypedConfig{TypedConfig: accessLogConf},
			},
		},
	})
	chains = append(chains, &listenerv3.FilterChain{
		Filters: []*listenerv3.Filter{
			{
				Name: "envoy.filters.network.tcp_proxy",
				ConfigType: &listenerv3.Filter_TypedConfig{
					TypedConfig: connMan,
				},
			},
		},
	})
	return chains
}

// udpProxyListenerFilter routes the datagrams to the backend. Envoy UDP listeners are connection-less: the
// proxy is a listener filter, a UDP listener with filter chains is rejected.
func udpProxyListenerFilter(vsUuid string, backendUuid string) *listenerv3.ListenerFilter {
	accessLogConf, _ := anypb.New(accessLogStdout) //nolint
	udpRoute := &udp_proxyv3.Route{
		Cluster: backendUuid,
	}
	udpRouteAny, _ := anypb.New(udpRoute) //nolint

	connMan, _ := anypb.New(&udp_proxyv3.UdpProxyConfig{
		StatPrefix: vsUuid,
		RouteSpecifier: &udp_proxyv3.UdpProxyConfig_Matcher{
			Matcher: &xds_matcher.Matcher{
				OnNoMatch: &xds_matcher.Matcher_OnMatch{
					OnMatch: &xds_matcher.Matcher_OnMatch_Action{
						Action: &xds_corev3.TypedExtensionConfig{
							Name:        "route",
							TypedConfig: udpRouteAny,
						},
					},
				},
			},
		},
		AccessLog: []*accesslogv3.AccessLog{
			{
				Name:       "envoy.access_loggers.stdout",
				ConfigType: &accesslogv3.AccessLog_TypedConfig{TypedConfig: accessLogConf},
			},
		},
		UpstreamSocketConfig: &corev3.UdpSocketConfig{
			MaxRxDatagramSize: &wrapperspb.UInt64Value{Value: rxDatagramSizeUdp},
		},
	})
	return &listenerv3.ListenerFilter{
		Name: "envoy.filters.udp_listener.udp_proxy",
		ConfigType: &listenerv3.ListenerFilter_TypedConfig{
			TypedConfig: connMan,
		},
	}
}

func virtualHostToFilterChain(vsUuid string, policies route.ExternalAuths, routes []*HttpRoute, opts *starling_vsopts.HttpOptions, rateLimit *starling_limit.StarlingRateLimit) []*listenerv3.FilterChain {
	httpMan := virtualHostToHttpManager(vsUuid, policies, routes, opts, rateLimit)
	httpManAny, _ := anypb.New(httpMan) // nolint
	hasTlsOpts := false
	for _, rte := range routes {
		if len(rte.VirtualHost.GetTlsOptions()) > 0 {
			hasTlsOpts = true
			break
		}
	}
	if !hasTlsOpts {
		return []*listenerv3.FilterChain{
			{
				Filters: []*listenerv3.Filter{
					{
						Name: "envoy.filters.network.http_connection_manager",
						ConfigType: &listenerv3.Filter_TypedConfig{
							TypedConfig: httpManAny,
						},
					},
				},
			},
		}
	}
	chains := make([]*listenerv3.FilterChain, 0)
	for _, rte := range routes {
		virtualHost := rte.VirtualHost
		for _, tlsOpt := range virtualHost.GetTlsOptions() {
			tlsContext, _ := anypb.New(&tlsv3.DownstreamTlsContext{
				CommonTlsContext: &tlsv3.CommonTlsContext{
					AlpnProtocols:                  tlsOpt.GetAlpnProtocols(),
					TlsCertificateSdsSecretConfigs: certificateUuidsToSds(tlsOpt.GetCertificatesUuid()),
					TlsParams: &tlsv3.TlsParameters{
						// already envoy's default, explicit to not depend on it
						TlsMinimumProtocolVersion: tlsv3.TlsParameters_TLSv1_2,
					},
				},
			})
			chains = append(chains, &listenerv3.FilterChain{
				FilterChainMatch: &listenerv3.FilterChainMatch{
					ServerNames: tlsOpt.GetServerNames(),
				},
				TransportSocket: &corev3.TransportSocket{
					Name: "envoy.transport_sockets.tls",
					ConfigType: &corev3.TransportSocket_TypedConfig{
						TypedConfig: tlsContext,
					},
				},
				Filters: []*listenerv3.Filter{
					{
						Name: "envoy.filters.network.http_connection_manager",
						ConfigType: &listenerv3.Filter_TypedConfig{
							TypedConfig: httpManAny,
						},
					},
				},
			})
		}
	}

	return chains
}

func certificateUuidsToSds(certUuids []string) []*tlsv3.SdsSecretConfig {
	sdsConfigs := make([]*tlsv3.SdsSecretConfig, 0, len(certUuids))
	for _, uuid := range certUuids {
		sdsConfigs = append(sdsConfigs, &tlsv3.SdsSecretConfig{
			Name: uuid,
			SdsConfig: &corev3.ConfigSource{
				ConfigSourceSpecifier: &corev3.ConfigSource_Ads{
					Ads: &corev3.AggregatedConfigSource{},
				},
				ResourceApiVersion: corev3.ApiVersion_V3,
			},
		})
	}
	return sdsConfigs
}

func virtualHostToHttpManager(vsUuid string, policies route.ExternalAuths, routes []*HttpRoute, opts *starling_vsopts.HttpOptions, rateLimit *starling_limit.StarlingRateLimit) *http_connection_managerv3.HttpConnectionManager {
	accessLogConf, _ := anypb.New(accessLogStdout) //nolint
	codecType := http_connection_managerv3.HttpConnectionManager_AUTO
	switch opts.CodecType {
	case starling_vsopts.HttpOptions_HTTP1:
		codecType = http_connection_managerv3.HttpConnectionManager_HTTP1
	case starling_vsopts.HttpOptions_HTTP2:
		codecType = http_connection_managerv3.HttpConnectionManager_HTTP2
	}
	router, _ := anypb.New(&routerv3.Router{})

	httpFilters := make([]*http_connection_managerv3.HttpFilter, 0)
	rateLimitFilter := localRateLimitFilter(vsUuid, rateLimit)
	if rateLimitFilter != nil {
		httpFilters = append(httpFilters, rateLimitFilter)
	}
	// after the rate limit, so the requests over a limit cost no check, and before the WAF, so a denied request costs
	// no WAF evaluation
	httpFilters = append(httpFilters, extAuthzFilters(policies, routes)...)
	wafFilter := wafFilter(routes)
	if wafFilter != nil {
		httpFilters = append(httpFilters, wafFilter)
	}
	httpFilters = append(httpFilters, &http_connection_managerv3.HttpFilter{
		Name: "envoy.filters.http.router",
		ConfigType: &http_connection_managerv3.HttpFilter_TypedConfig{
			TypedConfig: router,
		},
	})

	connMan := &http_connection_managerv3.HttpConnectionManager{
		CodecType:      codecType,
		StatPrefix:     vsUuid,
		RouteSpecifier: nil,
		AccessLog: []*accesslogv3.AccessLog{
			{
				Name:       "envoy.access_loggers.stdout",
				ConfigType: &accesslogv3.AccessLog_TypedConfig{TypedConfig: accessLogConf},
			},
		},
		HttpFilters:      httpFilters,
		UseRemoteAddress: &wrapperspb.BoolValue{Value: true},
		RepresentIpv4RemoteAddressAsIpv4MappedIpv6: false,
		NormalizePath:                &wrapperspb.BoolValue{Value: true},
		MergeSlashes:                 true,
		PathWithEscapedSlashesAction: http_connection_managerv3.HttpConnectionManager_UNESCAPE_AND_REDIRECT,
		// a virtual server has one port, the port in the host would only stop its domains from matching
		StripPortMode: &http_connection_managerv3.HttpConnectionManager_StripAnyHostPort{StripAnyHostPort: true},
		// envoy refuses an upgrade it isn't configured for
		UpgradeConfigs: []*http_connection_managerv3.HttpConnectionManager_UpgradeConfig{{UpgradeType: "websocket"}},
	}

	if opts.GetHttpProtocolOptions() != nil {
		connMan.HttpProtocolOptions = &corev3.Http1ProtocolOptions{
			EnableTrailers:        opts.GetHttpProtocolOptions().EnableTrailers,
			SendFullyQualifiedUrl: opts.GetHttpProtocolOptions().SendFullyQualifiedUrl,
		}
	}
	initStreamWindowsSize := &wrapperspb.UInt32Value{Value: 65536}    // as suggested for envoy as edge proxy
	initConnectWindowsSize := &wrapperspb.UInt32Value{Value: 1048576} // as suggested for envoy as edge proxy
	maxConcurrentStream := &wrapperspb.UInt32Value{Value: 1000000}
	if opts.GetHttp2ProtocolOptions() != nil {
		if opts.GetHttp2ProtocolOptions().InitialStreamWindowSize != nil {
			initStreamWindowsSize = opts.GetHttp2ProtocolOptions().InitialStreamWindowSize
		}
		if opts.GetHttp2ProtocolOptions().InitialConnectionWindowSize != nil {
			initConnectWindowsSize = opts.GetHttp2ProtocolOptions().InitialConnectionWindowSize
		}

		if opts.GetHttp2ProtocolOptions().MaxConcurrentStreams != nil {
			maxConcurrentStream = opts.GetHttp2ProtocolOptions().MaxConcurrentStreams
		}
		connMan.Http2ProtocolOptions = &corev3.Http2ProtocolOptions{
			HpackTableSize:                               opts.GetHttp2ProtocolOptions().HpackTableSize,
			MaxConcurrentStreams:                         maxConcurrentStream,
			InitialStreamWindowSize:                      initStreamWindowsSize,
			InitialConnectionWindowSize:                  initConnectWindowsSize,
			AllowConnect:                                 opts.GetHttp2ProtocolOptions().AllowConnect,
			MaxOutboundFrames:                            opts.GetHttp2ProtocolOptions().MaxOutboundFrames,
			MaxOutboundControlFrames:                     opts.GetHttp2ProtocolOptions().MaxOutboundControlFrames,
			MaxConsecutiveInboundFramesWithEmptyPayload:  opts.GetHttp2ProtocolOptions().MaxConsecutiveInboundFramesWithEmptyPayload,
			MaxInboundPriorityFramesPerStream:            opts.GetHttp2ProtocolOptions().MaxInboundPriorityFramesPerStream,
			MaxInboundWindowUpdateFramesPerDataFrameSent: opts.GetHttp2ProtocolOptions().MaxInboundWindowUpdateFramesPerDataFrameSent,
			ConnectionKeepalive:                          coreKeepaliveToEnvoyKeepalive(opts.GetHttp2ProtocolOptions().ConnectionKeepalive),
		}
	} else {
		connMan.Http2ProtocolOptions = &corev3.Http2ProtocolOptions{
			MaxConcurrentStreams:        maxConcurrentStream,
			InitialStreamWindowSize:     initStreamWindowsSize,
			InitialConnectionWindowSize: initConnectWindowsSize,
		}
	}
	if opts.GetHttp3ProtocolOptions() != nil {
		connMan.Http3ProtocolOptions = &corev3.Http3ProtocolOptions{
			QuicProtocolOptions:                     quicProtOptToQuicEnvoy(opts.GetHttp3ProtocolOptions().QuicProtocolOptions),
			OverrideStreamErrorOnInvalidHttpMessage: opts.GetHttp3ProtocolOptions().OverrideStreamErrorOnInvalidHttpMessage,
			AllowExtendedConnect:                    opts.GetHttp3ProtocolOptions().AllowExtendedConnect,
		}
	}
	idleTimeout := &durationpb.Duration{Seconds: 3600}
	if opts.GetCommonHttpProtocolOptions() != nil {
		if opts.GetCommonHttpProtocolOptions().IdleTimeout != nil {
			idleTimeout = opts.GetCommonHttpProtocolOptions().IdleTimeout
		}
		connMan.CommonHttpProtocolOptions = &corev3.HttpProtocolOptions{
			IdleTimeout:                  idleTimeout,
			MaxConnectionDuration:        opts.GetCommonHttpProtocolOptions().MaxConnectionDuration,
			MaxHeadersCount:              opts.GetCommonHttpProtocolOptions().MaxHeadersCount,
			MaxStreamDuration:            opts.GetCommonHttpProtocolOptions().MaxStreamDuration,
			HeadersWithUnderscoresAction: corev3.HttpProtocolOptions_REJECT_REQUEST,
			MaxRequestsPerConnection:     opts.GetCommonHttpProtocolOptions().MaxRequestsPerConnection,
		}
	} else {
		connMan.CommonHttpProtocolOptions = &corev3.HttpProtocolOptions{
			IdleTimeout:                  idleTimeout,
			HeadersWithUnderscoresAction: corev3.HttpProtocolOptions_REJECT_REQUEST,
		}
	}
	streamIdleTimeout := &durationpb.Duration{Seconds: 300}
	requestTimeout := &durationpb.Duration{Seconds: 0}
	if opts.GetStreamIdleTimeout() != nil {
		streamIdleTimeout = opts.GetStreamIdleTimeout()
	}
	if opts.GetRequestTimeout() != nil {
		requestTimeout = opts.GetRequestTimeout()
	}
	connMan.StreamIdleTimeout = streamIdleTimeout
	connMan.RequestTimeout = requestTimeout
	virtualHost := make([]*routev3.VirtualHost, 0, len(routes))
	for i, rte := range routes {
		virtualHost = append(virtualHost, &routev3.VirtualHost{
			Name:    rte.Name,
			Domains: rte.VirtualHost.GetDomains(),
			Routes:  virtualHostToRoutes(vsUuid, policies, i, rte.VirtualHost, opts),
		})
	}
	connMan.RouteSpecifier = &http_connection_managerv3.HttpConnectionManager_RouteConfig{
		RouteConfig: &routev3.RouteConfiguration{
			VirtualHosts:            virtualHost,
			ResponseHeadersToAdd:    headerValuesToEnvoyHeaderValues(opts.GetResponseHeadersToAdd()),
			ResponseHeadersToRemove: opts.GetResponseHeadersToRemove(),
			RequestHeadersToAdd:     headerValuesToEnvoyHeaderValues(opts.GetRequestHeadersToAdd()),
			RequestHeadersToRemove:  opts.GetRequestHeadersToRemove(),
		},
	}
	return connMan
}

func virtualHostToRoutes(vsUuid string, policies route.ExternalAuths, virtualHostIndex int, virtualHost *route.VirtualHost, opts *starling_vsopts.HttpOptions) []*routev3.Route {
	routes := make([]*routev3.Route, 0, len(virtualHost.GetRoutes()))
	for _, rte := range virtualHost.GetRoutes() {
		envoyRte := virtualHostRouteToEnvoyRoute(rte, virtualHost.GetPathPrefix(), opts)
		applyWafToRoute(vsUuid, virtualHostIndex, rte, envoyRte)
		applyExtAuthzToRoute(policies, virtualHost, rte, envoyRte)
		routes = append(routes, envoyRte)
	}
	return routes
}

func virtualHostRouteToEnvoyRoute(rte *route.Route, globalPathPrefix string, opts *starling_vsopts.HttpOptions) *routev3.Route {
	requestTimeout := &durationpb.Duration{Seconds: 0}
	if opts.GetRequestTimeout() != nil {
		requestTimeout = opts.GetRequestTimeout()
	}
	streamIdleTimeout := &durationpb.Duration{Seconds: 15}
	if opts.GetStreamIdleTimeout() != nil {
		streamIdleTimeout = opts.GetStreamIdleTimeout()
	}

	envoyRte := &routev3.Route{
		Name: rte.GetName(),
		Match: &routev3.RouteMatch{
			CaseSensitive: rte.GetMatch().GetCaseSensitive(),
		},
	}
	switch rm := rte.GetMatch().GetPathSpecifier().(type) {
	case *route.RouteMatch_Prefix:
		vhostPrefix := strings.TrimSuffix(globalPathPrefix, "/")
		isRouteOnPrefix := vhostPrefix != "" && (rm.Prefix == "/" || rm.Prefix == "")
		// the prefix "/api" alone would also match "/apiv2": a path separated prefix stops at a segment
		if isRouteOnPrefix {
			envoyRte.Match.PathSpecifier = &routev3.RouteMatch_PathSeparatedPrefix{
				PathSeparatedPrefix: vhostPrefix,
			}
			break
		}
		envoyRte.Match.PathSpecifier = &routev3.RouteMatch_Prefix{
			Prefix: joinPathPrefix(globalPathPrefix, rm.Prefix),
		}
	case *route.RouteMatch_Path:
		envoyRte.Match.PathSpecifier = &routev3.RouteMatch_Path{
			Path: joinPathPrefix(globalPathPrefix, rm.Path),
		}
	case *route.RouteMatch_SafeRegex:
		envoyRte.Match.PathSpecifier = &routev3.RouteMatch_SafeRegex{
			SafeRegex: &matcherv3.RegexMatcher{
				Regex: rm.SafeRegex,
			},
		}
	}
	switch act := rte.GetAction().(type) {
	case *route.Route_Redirect:
		redirAct := &routev3.RedirectAction{
			HostRedirect: act.Redirect.GetHostRedirect(),
			PortRedirect: act.Redirect.GetPortRedirect(),
		}
		if act.Redirect.GetPathRedirect() != "" {
			redirAct.PathRewriteSpecifier = &routev3.RedirectAction_PathRedirect{PathRedirect: act.Redirect.GetPathRedirect()}
		}
		if act.Redirect.GetSchemeRedirect() != "" {
			redirAct.SchemeRewriteSpecifier = &routev3.RedirectAction_SchemeRedirect{
				SchemeRedirect: act.Redirect.GetSchemeRedirect(),
			}
		}
		if act.Redirect.GetHttpsRedirect() {
			redirAct.SchemeRewriteSpecifier = &routev3.RedirectAction_HttpsRedirect{
				HttpsRedirect: act.Redirect.GetHttpsRedirect(),
			}
		}

		envoyRte.Action = &routev3.Route_Redirect{
			Redirect: redirAct,
		}

	case *route.Route_DirectResponse:
		dataSource := &corev3.DataSource{
			Specifier: &corev3.DataSource_InlineBytes{
				InlineBytes: act.DirectResponse.GetInlineBytes(),
			},
		}
		if act.DirectResponse.GetInlineString() != "" {
			dataSource.Specifier = &corev3.DataSource_InlineString{
				InlineString: act.DirectResponse.GetInlineString(),
			}
		}
		envoyRte.Action = &routev3.Route_DirectResponse{
			DirectResponse: &routev3.DirectResponseAction{
				Status: act.DirectResponse.GetStatus(),
				Body:   dataSource,
			},
		}
	case *route.Route_BackendUuid:
		// the http route handler refuses the routes whose options can't be read
		routeOpts, _ := rte.GetStarlingOptions()
		envoyRte.Action = &routev3.Route_Route{
			Route: &routev3.RouteAction{
				ClusterSpecifier: &routev3.RouteAction_Cluster{
					Cluster: act.BackendUuid,
				},
				Timeout:     requestTimeout,
				IdleTimeout: streamIdleTimeout,
				RateLimits:  routeRateLimitsToEnvoy(routeOpts.GetRateLimits()),
			},
		}

	}
	return envoyRte
}

func headerValuesToEnvoyHeaderValues(headerValues []*core.HeaderValueOption) []*corev3.HeaderValueOption {
	envoyHeaderValues := make([]*corev3.HeaderValueOption, 0, len(headerValues))
	for _, headerValue := range headerValues {
		appendAction := corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD
		if headerValue.Append {
			appendAction = corev3.HeaderValueOption_APPEND_IF_EXISTS_OR_ADD
		}
		envoyHeaderValues = append(envoyHeaderValues, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{
				Key:   headerValue.Header.Key,
				Value: headerValue.Header.GetValue(),
			},
			AppendAction: appendAction,
		})
	}
	return envoyHeaderValues
}

func makeTlsInspectorListFilt() *listenerv3.ListenerFilter {
	inspector, _ := anypb.New(&tls_inspectorv3.TlsInspector{}) //nolint
	return &listenerv3.ListenerFilter{
		Name: "envoy.filters.listener.tls_inspector",
		ConfigType: &listenerv3.ListenerFilter_TypedConfig{
			TypedConfig: inspector,
		},
	}
}

func proxyProtocolListFilters(accept bool) []*listenerv3.ListenerFilter {
	if !accept {
		return []*listenerv3.ListenerFilter{}
	}
	filter, _ := anypb.New(&proxy_protocolv3.ProxyProtocol{}) //nolint
	return []*listenerv3.ListenerFilter{{
		Name: "envoy.filters.listener.proxy_protocol",
		ConfigType: &listenerv3.ListenerFilter_TypedConfig{
			TypedConfig: filter,
		},
	}}
}

func makeOriginSrcListFilt() *listenerv3.ListenerFilter {
	filter, _ := anypb.New(&original_srcv3.OriginalSrc{}) //nolint
	return &listenerv3.ListenerFilter{
		Name: "envoy.filters.listener.original_src",
		ConfigType: &listenerv3.ListenerFilter_TypedConfig{
			TypedConfig: filter,
		},
	}
}

func makeOriginDstListFilt() *listenerv3.ListenerFilter {
	filter, _ := anypb.New(&original_dstv3.OriginalDst{}) //nolint
	return &listenerv3.ListenerFilter{
		Name: "envoy.filters.listener.original_dst",
		ConfigType: &listenerv3.ListenerFilter_TypedConfig{
			TypedConfig: filter,
		},
	}
}

// joinPathPrefix puts the path prefix of a virtual host before the path of one of its routes: the route "/"
// is the prefix itself, so that "/api" is matched and not only "/api/".
func joinPathPrefix(pathPrefix, path string) string {
	if pathPrefix == "" {
		return path
	}
	if path == "/" || path == "" {
		return pathPrefix
	}
	return strings.TrimSuffix(pathPrefix, "/") + path
}
