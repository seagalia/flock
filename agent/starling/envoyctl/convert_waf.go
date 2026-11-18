package envoyctl

import (
	"strconv"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	ext_procv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	http_connection_managerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	starling_route "github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	wafFilterName = "envoy.filters.http.ext_proc"
	// Same key as wafproccessor.MetadataKeyWafUUID, the WAF picks the config of a stream with it.
	wafMetadataKey = "x-waf-uuid"
	// Same values as wafproccessor.envoyMessageTimeout and envoyMaxMessageTimeout. The WAF answers most messages
	// within the first, and asks for up to the second for a request body the CRS needs longer to inspect.
	wafMessageTimeout    = 200 * time.Millisecond
	wafMaxMessageTimeout = 10 * time.Second
)

// WafIdentifier is the identifier the config of a route is registered under in the WAF. The index of the virtual host
// keeps apart the routes of the same name in two virtual hosts, a route name being unique only in its virtual host.
func WafIdentifier(vsUuid string, virtualHostIndex int, routeName string) string {
	return vsUuid + "-" + strconv.Itoa(virtualHostIndex) + "-" + routeName
}

// wafFilter is disabled on the listener because its routes don't all have a WAF. The routes with one enable it with
// their per filter config (applyWafToRoute), which also sets their body modes.
func wafFilter(routes []*HttpRoute) *http_connection_managerv3.HttpFilter {
	if !hasWafRoute(routes) {
		return nil
	}
	extProc, _ := anypb.New(&ext_procv3.ExternalProcessor{ // nolint
		GrpcService: &corev3.GrpcService{
			TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
				EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{ClusterName: wafClusterName},
			},
		},
		FailureModeAllow:  false,
		MessageTimeout:    durationpb.New(wafMessageTimeout),
		MaxMessageTimeout: durationpb.New(wafMaxMessageTimeout),
		ProcessingMode: &ext_procv3.ProcessingMode{
			RequestHeaderMode:  ext_procv3.ProcessingMode_SEND,
			ResponseHeaderMode: ext_procv3.ProcessingMode_SEND,
			// STREAMED would forward each chunk before the phase 2 and 4 rules run at the end of the body. A request
			// over the buffer limit of its route is refused (like SecRequestBodyLimitAction Reject), a response over
			// the 32 KiB of the backend buffer is inspected up to it (like SecResponseBodyLimitAction ProcessPartial).
			RequestBodyMode:  ext_procv3.ProcessingMode_BUFFERED,
			ResponseBodyMode: ext_procv3.ProcessingMode_BUFFERED_PARTIAL,
		},
		RequestAttributes: []string{
			"source.address",
			"source.port",
			"destination.address",
			"destination.port",
			"request.scheme",
			"request.host",
			"request.path",
			"request.method",
			"request.protocol",
			"response.code",
		},
	})
	return &http_connection_managerv3.HttpFilter{
		Name:     wafFilterName,
		Disabled: true,
		ConfigType: &http_connection_managerv3.HttpFilter_TypedConfig{
			TypedConfig: extProc,
		},
	}
}

func hasWafRoute(routes []*HttpRoute) bool {
	for _, rte := range routes {
		for _, vhostRoute := range rte.VirtualHost.GetRoutes() {
			// the http route handler refuses the routes whose options can't be read
			routeOpts, _ := vhostRoute.GetStarlingOptions()
			if routeOpts.GetWaf() != nil {
				return true
			}
		}
	}
	return false
}

func applyWafToRoute(vsUuid string, virtualHostIndex int, rte *route.Route, envoyRte *routev3.Route) {
	routeOpts, _ := rte.GetStarlingOptions()
	waf := routeOpts.GetWaf()
	if waf == nil {
		return
	}

	requestBodyLimit := uint64(waf.GetRequestBodyLimit())
	if requestBodyLimit == 0 {
		requestBodyLimit = starling_route.DefaultWafRequestBodyLimit
	}

	envoyRte.RequestBodyBufferLimit = wrapperspb.UInt64(requestBodyLimit)

	requestBodyMode := ext_procv3.ProcessingMode_BUFFERED
	if waf.GetPartialRequestBody() {
		requestBodyMode = ext_procv3.ProcessingMode_BUFFERED_PARTIAL
	}
	responseBodyMode := ext_procv3.ProcessingMode_BUFFERED_PARTIAL
	if waf.GetSkipResponseBody() {
		responseBodyMode = ext_procv3.ProcessingMode_NONE
	}
	overrides := &ext_procv3.ExtProcOverrides{
		ProcessingMode: &ext_procv3.ProcessingMode{
			RequestHeaderMode:  ext_procv3.ProcessingMode_SEND,
			ResponseHeaderMode: ext_procv3.ProcessingMode_SEND,
			RequestBodyMode:    requestBodyMode,
			ResponseBodyMode:   responseBodyMode,
		},
		FailureModeAllow: wrapperspb.Bool(waf.GetFailOpen()),
	}
	hasOwnConfig := waf.GetDetectOnly() || waf.GetCustomRules() != ""
	if hasOwnConfig {
		overrides.GrpcInitialMetadata = []*corev3.HeaderValue{{
			Key:   wafMetadataKey,
			Value: WafIdentifier(vsUuid, virtualHostIndex, rte.GetName()),
		}}
	}
	perRoute, _ := anypb.New(&ext_procv3.ExtProcPerRoute{ // nolint
		Override: &ext_procv3.ExtProcPerRoute_Overrides{Overrides: overrides},
	})
	if envoyRte.TypedPerFilterConfig == nil {
		envoyRte.TypedPerFilterConfig = make(map[string]*anypb.Any)
	}
	envoyRte.TypedPerFilterConfig[wafFilterName] = perRoute
}
