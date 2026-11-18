package envoyctl

import (
	mutation_rulesv3 "github.com/envoyproxy/go-control-plane/envoy/config/common/mutation_rules/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	ext_authzv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_authz/v3"
	header_mutationv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/header_mutation/v3"
	http_connection_managerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	// outside the flock.ext_authz. namespace of the policy filters, so a policy named "headers" can't collide with it
	extAuthzHeadersFilterName = "flock.ext_authz_headers"
)

var (
	// set on the check request only, by headers_to_add of each ext_authz filter, so the request sent to the backend is
	// left as the client sent it. Envoy overrides a client header of the same name with them. x-forwarded-for carries
	// the address the agent sees (after the proxy protocol), never a value the client sent.
	extAuthzForwardedHeaders = []*corev3.HeaderValue{
		{Key: "x-forwarded-method", Value: "%REQ(:method)%"},
		{Key: "x-forwarded-host", Value: "%REQ(:authority)%"},
		{Key: "x-forwarded-uri", Value: "%REQ(:path)%"},
		{Key: "x-forwarded-proto", Value: "%REQ(:scheme)%"},
		{Key: "x-forwarded-for", Value: "%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%"},
	}
	// always passed on a deny, so a check service can redirect to a login page or ask for credentials
	extAuthzDenyHeaders = []string{"location", "www-authenticate"}
)

func extAuthzFilterName(policyName string) string {
	return "flock.ext_authz." + policyName
}

// extAuthzFilters gives the header mutation filter, enabled on every route as soon as the virtual server has a policy,
// then one disabled ext_authz filter per policy a rendered route uses, which applyExtAuthzToRoute enables on its
// routes. A policy needs its own filter since failure_mode_allow and status_on_error can't be set per route.
func extAuthzFilters(policies route.ExternalAuths, routes []*HttpRoute) []*http_connection_managerv3.HttpFilter {
	if len(policies) == 0 {
		return nil
	}
	filters := []*http_connection_managerv3.HttpFilter{extAuthzHeadersFilter(policies)}
	for _, policy := range usedExternalAuths(policies, routes) {
		filters = append(filters, extAuthzFilter(policy))
	}
	return filters
}

func usedExternalAuths(policies route.ExternalAuths, routes []*HttpRoute) []*route.ExternalAuth {
	vhosts := make([]*route.VirtualHost, 0, len(routes))
	for _, rte := range routes {
		vhosts = append(vhosts, rte.VirtualHost)
	}
	return policies.Used(vhosts)
}

func extAuthzHeadersFilter(policies route.ExternalAuths) *http_connection_managerv3.HttpFilter {
	mutations := make([]*mutation_rulesv3.HeaderMutation, 0, len(policies.StrippedHeaders()))
	for _, header := range policies.StrippedHeaders() {
		mutations = append(mutations, &mutation_rulesv3.HeaderMutation{
			Action: &mutation_rulesv3.HeaderMutation_Remove{Remove: header},
		})
	}
	headerMutation, _ := anypb.New(&header_mutationv3.HeaderMutation{ // nolint
		Mutations: &header_mutationv3.Mutations{RequestMutations: mutations},
	})
	return &http_connection_managerv3.HttpFilter{
		Name:       extAuthzHeadersFilterName,
		ConfigType: &http_connection_managerv3.HttpFilter_TypedConfig{TypedConfig: headerMutation},
	}
}

func extAuthzFilter(policy *route.ExternalAuth) *http_connection_managerv3.HttpFilter {
	clientHeaders := append([]string{}, policy.GetClientHeaders()...)
	clientHeaders = append(clientHeaders, extAuthzDenyHeaders...)

	authorizationResponse := &ext_authzv3.AuthorizationResponse{
		AllowedClientHeaders: exactHeaderMatchers(clientHeaders),
	}
	if len(policy.GetUpstreamHeaders()) > 0 {
		authorizationResponse.AllowedUpstreamHeaders = exactHeaderMatchers(policy.GetUpstreamHeaders())
	}
	// envoy refuses an empty matcher list, and with none set it still sends its defaults, authorization among them
	var allowedHeaders *matcherv3.ListStringMatcher
	if len(policy.GetRequestHeaders()) > 0 {
		allowedHeaders = exactHeaderMatchers(policy.GetRequestHeaders())
	}
	extAuthz, _ := anypb.New(&ext_authzv3.ExtAuthz{ // nolint
		Services: &ext_authzv3.ExtAuthz_HttpService{HttpService: &ext_authzv3.HttpService{
			// HttpUri requires a uri, the cluster (named after the backend uuid) is what routes the check
			ServerUri: &corev3.HttpUri{
				Uri:              "http://" + policy.GetBackendUuid(),
				HttpUpstreamType: &corev3.HttpUri_Cluster{Cluster: policy.GetBackendUuid()},
				Timeout:          policy.GetTimeout(),
			},
			PathOverride:          policy.GetPath(),
			AuthorizationRequest:  &ext_authzv3.AuthorizationRequest{HeadersToAdd: extAuthzForwardedHeaders},
			AuthorizationResponse: authorizationResponse,
		}},
		AllowedHeaders:   allowedHeaders,
		FailureModeAllow: policy.GetFailureMode() == route.FailureMode_ALLOW,
		StatusOnError:    &typev3.HttpStatus{Code: typev3.StatusCode(policy.GetStatusOnError())},
	})
	return &http_connection_managerv3.HttpFilter{
		Name:       extAuthzFilterName(policy.GetName()),
		Disabled:   true,
		ConfigType: &http_connection_managerv3.HttpFilter_TypedConfig{TypedConfig: extAuthz},
	}
}

func exactHeaderMatchers(headers []string) *matcherv3.ListStringMatcher {
	patterns := make([]*matcherv3.StringMatcher, 0, len(headers))
	for _, header := range headers {
		patterns = append(patterns, &matcherv3.StringMatcher{
			MatchPattern: &matcherv3.StringMatcher_Exact{Exact: header},
			IgnoreCase:   true,
		})
	}
	return &matcherv3.ListStringMatcher{Patterns: patterns}
}

func applyExtAuthzToRoute(policies route.ExternalAuths, vh *route.VirtualHost, rte *route.Route, envoyRte *routev3.Route) {
	policy := policies.Of(vh, rte)
	if policy == nil {
		return
	}
	perRoute, _ := anypb.New(&ext_authzv3.ExtAuthzPerRoute{ // nolint
		Override: &ext_authzv3.ExtAuthzPerRoute_CheckSettings{CheckSettings: &ext_authzv3.CheckSettings{}},
	})
	if envoyRte.TypedPerFilterConfig == nil {
		envoyRte.TypedPerFilterConfig = make(map[string]*anypb.Any)
	}
	envoyRte.TypedPerFilterConfig[extAuthzFilterName(policy.GetName())] = perRoute
}
