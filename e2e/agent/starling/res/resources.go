//go:build e2e

package res

import (
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/limit/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	starling_commopts "github.com/seagalia/flock/sdk/flock/extensions/common/starling_options/v1"
	starling_limit "github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	starling_route "github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

func mustAny(m proto.Message) *anypb.Any {
	a, err := anypb.New(m)
	if err != nil {
		panic(err)
	}
	return a
}

func Vip(uuid, ip string, announce starling_announce.StarlingAnnounce_TypeAnnounce, vsUuids ...string) *vip.VIP {
	return &vip.VIP{
		UUID:               uuid,
		Name:               uuid,
		Ip:                 ip,
		VirtualServersUuid: vsUuids,
		TypedAnnounce:      mustAny(&starling_announce.StarlingAnnounce{TypeAnnounce: announce}),
	}
}

type BackendOpt func(*backend.Backend)

func WithProxyProtocolV2() BackendOpt {
	return func(c *backend.Backend) {
		c.EnableProxyProtocol = true
		c.ProxyProtocolVersion = backend.Backend_V2
	}
}

func WithLbPolicy(policy backend.Backend_LbPolicy) BackendOpt {
	return func(c *backend.Backend) { c.LbPolicy = policy }
}

func WithNoHealthCheck() BackendOpt {
	return func(c *backend.Backend) {
		c.HealthCheck.HealthChecker = &core.HealthCheck_NoHealthCheck{NoHealthCheck: &core.NoHealthCheck{}}
	}
}

// WithEds replaces the static endpoints with the ones an endpoint discovery server gives for service at url
func WithEds(url, service string) BackendOpt {
	return func(c *backend.Backend) {
		coreUrl, err := core.UrlToCoreUrl(url)
		if err != nil {
			panic(err)
		}
		c.Endpoint = &backend.Backend_Eds{Eds: &backend.EDS{Url: coreUrl, ServiceName: service}}
		c.RefreshRate = durationpb.New(time.Second)
	}
}

// WithDns replaces the static endpoints with the ipv4 addresses of host resolved by nameserver
func WithDns(host string, port uint32, nameserver string) BackendOpt {
	return func(c *backend.Backend) {
		c.Endpoint = &backend.Backend_Dns{Dns: &backend.DNS{
			Host:         host,
			Port:         port,
			LookupFamily: backend.DNS_V4_ONLY,
			Nameservers:  []*core.IPAddr{{Ip: nameserver, Port: 53}},
		}}
		c.RefreshRate = durationpb.New(time.Second)
	}
}

// WithUpstreamHttp2 makes envoy talk h2 over tls to the endpoints, verified with ca for sni
func WithUpstreamHttp2(caPem []byte, sni string) BackendOpt {
	return func(c *backend.Backend) {
		c.EnableTls = true
		c.Ca = string(caPem)
		c.Sni = sni
		c.HttpProtocolOptions = mustAny(&starling_commopts.HttpProtocolOptions{
			UpstreamProtocolOptions: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_{
				ExplicitHttpConfig: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig{
					ProtocolConfig: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
						Http2ProtocolOptions: &starling_commopts.Http2ProtocolOptions{},
					},
				},
			},
		})
	}
}

// Backend is round robin and tcp health checked every second
func Backend(uuid string, endpoints []string, opts ...BackendOpt) *backend.Backend {
	ipAddrs := make([]*core.IPAddr, 0, len(endpoints))
	for _, endpoint := range endpoints {
		host, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			panic(err)
		}
		portNum, err := strconv.Atoi(port)
		if err != nil {
			panic(err)
		}
		ipAddrs = append(ipAddrs, &core.IPAddr{Ip: host, Port: uint32(portNum)})
	}
	c := &backend.Backend{
		UUID:           uuid,
		Name:           uuid,
		ConnectTimeout: durationpb.New(5 * time.Second),
		LbPolicy:       backend.Backend_ROUND_ROBIN,
		Endpoint:       &backend.Backend_IpAddrGroup{IpAddrGroup: &core.IPAddrGroup{IpAddresses: ipAddrs}},
		HealthCheck: &core.HealthCheck{
			Timeout:            durationpb.New(time.Second),
			Interval:           durationpb.New(time.Second),
			HealthyThreshold:   1,
			UnhealthyThreshold: 1,
			HealthChecker:      &core.HealthCheck_TcpHealthCheck{TcpHealthCheck: &core.TcpHealthCheck{}},
		},
		RefreshRate: durationpb.New(10 * time.Second),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type VsOpt func(*vs.VirtualServer)

func WithConnLimit(maxConn uint64) VsOpt {
	return func(v *vs.VirtualServer) {
		v.LimitConfig = append(v.LimitConfig, &limit.Config{Action: &limit.Config_ConnectionLimit{
			ConnectionLimit: &limit.ConnectionLimit{MaxConnections: maxConn, DestinationAddress: true},
		}})
	}
}

func WithSourceConnLimit(maxConn uint64) VsOpt {
	return func(v *vs.VirtualServer) {
		v.LimitConfig = append(v.LimitConfig, &limit.Config{Action: &limit.Config_ConnectionLimit{
			ConnectionLimit: &limit.ConnectionLimit{MaxConnections: maxConn, SourceAddress: true},
		}})
	}
}

// WithRateLimit is in new connections per second
func WithRateLimit(rate, burst uint64) VsOpt {
	return func(v *vs.VirtualServer) {
		v.LimitConfig = append(v.LimitConfig, &limit.Config{Action: &limit.Config_ConnectionRateLimit{
			ConnectionRateLimit: &limit.ConnectionRateLimit{Rate: rate, Burst: burst, Unit: flocktype.TimeUnit_SECOND, DestinationAddress: true},
		}})
	}
}

func WithRequestRateLimitByHeader(header string, requestsPerMinute uint32) VsOpt {
	return func(v *vs.VirtualServer) {
		routeOpts := mustAny(&starling_route.VirtualHostRouteOptions{RateLimits: []*starling_route.RouteRateLimit{{
			Actions: []*starling_route.RouteRateLimit_Action{{ActionSpecifier: &starling_route.RouteRateLimit_Action_RequestHeaders_{
				RequestHeaders: &starling_route.RouteRateLimit_Action_RequestHeaders{HeaderName: header, DescriptorKey: header},
			}}},
		}}})
		for _, vh := range v.GetHttpRoutesConfig().GetVirtualHosts() {
			for _, rte := range vh.GetRoutes() {
				rte.TypedOptions = routeOpts
			}
		}
		v.LimitConfig = append(v.LimitConfig, &limit.Config{Action: &limit.Config_TypedLimit{
			TypedLimit: mustAny(&starling_limit.StarlingRateLimit{Descriptors: []*starling_limit.Descriptor{{
				Key:    header,
				Policy: &starling_limit.Policy{Unit: flocktype.TimeUnit_MINUTE, RequestsPerUnit: requestsPerMinute},
			}}}),
		}})
	}
}

func WithL3Log() VsOpt {
	return func(v *vs.VirtualServer) { v.EnableL3Log = true }
}

func WithTcpUdpOptions(disableSnat, disableDnat bool) VsOpt {
	return func(v *vs.VirtualServer) {
		opts := mustAny(&starling_vsopts.TcpUdpOptions{DisableSnat: disableSnat, DisableDnat: disableDnat})
		switch rte := v.GetRouteConfig().(type) {
		case *vs.VirtualServer_TcpRouteConfig:
			rte.TcpRouteConfig.TypedOptions = opts
		case *vs.VirtualServer_UdpRouteConfig:
			rte.UdpRouteConfig.TypedOptions = opts
		}
	}
}

// WithAcceptProxyProtocol makes an http, tls_tcp or tcp virtual server require a proxy protocol header
func WithAcceptProxyProtocol() VsOpt {
	return func(v *vs.VirtualServer) {
		switch rte := v.GetRouteConfig().(type) {
		case *vs.VirtualServer_HttpRoutesConfig:
			rte.HttpRoutesConfig.AcceptProxyProtocol = true
		case *vs.VirtualServer_TlsTcpRoutesConfig:
			rte.TlsTcpRoutesConfig.AcceptProxyProtocol = true
		case *vs.VirtualServer_TcpRouteConfig:
			rte.TcpRouteConfig.AcceptProxyProtocol = true
		}
	}
}

// WithExternalAuths gives the policies the defaults and lowercase header names the controller would
func WithExternalAuths(policies ...*route.ExternalAuth) VsOpt {
	return func(v *vs.VirtualServer) {
		for _, policy := range policies {
			policy.ProvDefault()
		}
		httpConfig := v.GetHttpRoutesConfig()
		httpConfig.ExternalAuths = append(httpConfig.ExternalAuths, policies...)
	}
}

func applyVs(v *vs.VirtualServer, opts []VsOpt) *vs.VirtualServer {
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// Apply is for constructors taking routes as variadic parameter, they have no room for options
func Apply(v *vs.VirtualServer, opts ...VsOpt) *vs.VirtualServer {
	return applyVs(v, opts)
}

func TcpVs(uuid string, port uint32, backendUuid string, opts ...VsOpt) *vs.VirtualServer {
	return applyVs(&vs.VirtualServer{
		UUID: uuid, Name: uuid, Port: port,
		RouteConfig: &vs.VirtualServer_TcpRouteConfig{TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: backendUuid}},
	}, opts)
}

func UdpVs(uuid string, port uint32, backendUuid string, opts ...VsOpt) *vs.VirtualServer {
	return applyVs(&vs.VirtualServer{
		UUID: uuid, Name: uuid, Port: port,
		RouteConfig: &vs.VirtualServer_UdpRouteConfig{UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: backendUuid}},
	}, opts)
}

// HttpRoute gives vh with a last route sending everything else to the backend, under the path prefix of vh.
// An empty backendUuid is for virtual hosts with only redirects or direct responses.
func HttpRoute(vh *route.VirtualHost, backendUuid string) *route.VirtualHost {
	if backendUuid == "" {
		return vh
	}
	vh.Routes = append(vh.Routes, &route.Route{
		Name:   "default",
		Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
		Action: &route.Route_BackendUuid{BackendUuid: backendUuid},
	})
	return vh
}

func HttpVs(uuid string, port uint32, vhosts ...*route.VirtualHost) *vs.VirtualServer {
	return &vs.VirtualServer{
		UUID: uuid, Name: uuid, Port: port,
		RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{VirtualHosts: vhosts}},
	}
}

func TlsTcpRoute(backendUuid, certUuid string, serverNames ...string) *vs.TlsTcpRouteConfig {
	return &vs.TlsTcpRouteConfig{
		BackendUuid: backendUuid,
		Mode:        &vs.TlsTcpRouteConfig_Tls{Tls: &core.TLS{ServerNames: serverNames, CertificatesUuid: []string{certUuid}}},
	}
}

func TlsPassthroughRoute(backendUuid string, serverNames ...string) *vs.TlsTcpRouteConfig {
	return &vs.TlsTcpRouteConfig{
		BackendUuid: backendUuid,
		Mode:        &vs.TlsTcpRouteConfig_Passthrough{Passthrough: &vs.TlsPassthrough{ServerNames: serverNames}},
	}
}

func TlsTcpVs(uuid string, port uint32, routes ...*vs.TlsTcpRouteConfig) *vs.VirtualServer {
	return &vs.VirtualServer{
		UUID: uuid, Name: uuid, Port: port,
		RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: routes}},
	}
}

type VhOpt func(*route.VirtualHost)

func WithPathPrefix(prefix string) VhOpt {
	return func(vh *route.VirtualHost) { vh.PathPrefix = prefix }
}

// WithRedirect takes an absolute url as location
func WithRedirect(name, prefix, location string) VhOpt {
	return func(vh *route.VirtualHost) {
		u, err := url.Parse(location)
		if err != nil {
			panic(err)
		}
		redirect := &route.RedirectAction{HostRedirect: u.Host, PathRedirect: u.Path}
		if u.Scheme != "" {
			redirect.SchemeRewriteSpecifier = &route.RedirectAction_SchemeRedirect{SchemeRedirect: u.Scheme}
		}
		vh.Routes = append(vh.Routes, &route.Route{
			Name:   name,
			Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: prefix}},
			Action: &route.Route_Redirect{Redirect: redirect},
		})
	}
}

// WithDirectResponse matches the exact path
func WithDirectResponse(name, path string, status uint32, body string) VhOpt {
	return func(vh *route.VirtualHost) {
		vh.Routes = append(vh.Routes, &route.Route{
			Name:  name,
			Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Path{Path: path}},
			Action: &route.Route_DirectResponse{DirectResponse: &route.DirectResponseAction{
				Status:    status,
				Specifier: &route.DirectResponseAction_InlineString{InlineString: body},
			}},
		})
	}
}

// WithWafRoute sends the paths under prefix to the backend through the WAF, with its default config when waf has none.
// A nil waf gives a route without WAF.
func WithWafRoute(name, prefix, backendUuid string, waf *starling_route.Waf) VhOpt {
	return func(vh *route.VirtualHost) {
		rte := &route.Route{
			Name:   name,
			Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: prefix}},
			Action: &route.Route_BackendUuid{BackendUuid: backendUuid},
		}
		if waf != nil {
			rte.TypedOptions = mustAny(&starling_route.VirtualHostRouteOptions{Waf: waf})
		}
		vh.Routes = append(vh.Routes, rte)
	}
}

func WithVhostExternalAuth(name string) VhOpt {
	return func(vh *route.VirtualHost) { vh.ExternalAuth = name }
}

// WithRouteExternalAuth sends the paths under prefix to the backend once policy allowed them, whatever the default
// of the virtual host
func WithRouteExternalAuth(name, prefix, backendUuid, policy string) VhOpt {
	return func(vh *route.VirtualHost) {
		vh.Routes = append(vh.Routes, &route.Route{
			Name:         name,
			Match:        &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: prefix}},
			Action:       &route.Route_BackendUuid{BackendUuid: backendUuid},
			ExternalAuth: &route.Route_ExternalAuthName{ExternalAuthName: policy},
		})
	}
}

// WithRouteExternalAuthDisabled sends the paths under prefix to the backend with no check
func WithRouteExternalAuthDisabled(name, prefix, backendUuid string) VhOpt {
	return func(vh *route.VirtualHost) {
		vh.Routes = append(vh.Routes, &route.Route{
			Name:         name,
			Match:        &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: prefix}},
			Action:       &route.Route_BackendUuid{BackendUuid: backendUuid},
			ExternalAuth: &route.Route_ExternalAuthDisabled{ExternalAuthDisabled: true},
		})
	}
}

func WithTls(certUuid string, serverNames ...string) VhOpt {
	return func(vh *route.VirtualHost) {
		vh.TlsOptions = append(vh.TlsOptions, &core.TLS{ServerNames: serverNames, CertificatesUuid: []string{certUuid}})
	}
}

func VirtualHost(domains []string, opts ...VhOpt) *route.VirtualHost {
	vh := &route.VirtualHost{Domains: domains}
	for _, opt := range opts {
		opt(vh)
	}
	return vh
}

func Certificate(uuid string, certPem, keyPem []byte) *cert.Certificate {
	return &cert.Certificate{UUID: uuid, Name: uuid, CertChain: string(certPem), PrivateKey: string(keyPem)}
}
