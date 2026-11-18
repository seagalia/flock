package envoyctl

import (
	"time"

	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	ext_authzv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_authz/v3"
	header_mutationv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/header_mutation/v3"
	http_connection_managerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_limit "github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	starling_route "github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

var _ = Describe("External auth conversion", func() {
	var cfg *vs.HttpRoutesConfig

	headerMutationOf := func(filter *http_connection_managerv3.HttpFilter) *header_mutationv3.HeaderMutation {
		GinkgoHelper()
		return unmarshalAny(filter.GetTypedConfig(), &header_mutationv3.HeaderMutation{})
	}
	removedHeaders := func(mutation *header_mutationv3.HeaderMutation) []string {
		removed := make([]string, 0)
		for _, m := range mutation.GetMutations().GetRequestMutations() {
			if m.GetRemove() != "" {
				removed = append(removed, m.GetRemove())
			}
		}
		return removed
	}
	headersToAdd := func(authz *ext_authzv3.ExtAuthz) map[string]string {
		added := make(map[string]string)
		for _, header := range authz.GetHttpService().GetAuthorizationRequest().GetHeadersToAdd() {
			added[header.GetKey()] = header.GetValue()
		}
		return added
	}
	extAuthzOf := func(filter *http_connection_managerv3.HttpFilter) *ext_authzv3.ExtAuthz {
		GinkgoHelper()
		return unmarshalAny(filter.GetTypedConfig(), &ext_authzv3.ExtAuthz{})
	}
	exactPatterns := func(list *matcherv3.ListStringMatcher) []string {
		GinkgoHelper()
		patterns := make([]string, 0, len(list.GetPatterns()))
		for _, pattern := range list.GetPatterns() {
			Expect(pattern.GetIgnoreCase()).To(BeTrue())
			patterns = append(patterns, pattern.GetExact())
		}
		return patterns
	}
	filterNames := func(filters []*http_connection_managerv3.HttpFilter) []string {
		names := make([]string, 0, len(filters))
		for _, filter := range filters {
			names = append(names, filter.GetName())
		}
		return names
	}
	httpRoutesOf := func(httpConfig *vs.HttpRoutesConfig) []*HttpRoute {
		routes := make([]*HttpRoute, 0, len(httpConfig.GetVirtualHosts()))
		for _, vh := range httpConfig.GetVirtualHosts() {
			routes = append(routes, &HttpRoute{
				Name:        "vs-0",
				VirtualHost: vh,
				Backends:    []*backend.Backend{{UUID: "backend-1"}},
			})
		}
		return routes
	}
	managerOf := func(httpConfig *vs.HttpRoutesConfig, rateLimit *starling_limit.StarlingRateLimit) *http_connection_managerv3.HttpConnectionManager {
		GinkgoHelper()
		httpMan := virtualHostToHttpManager("vs-uuid", httpConfig.GetExternalAuths(), httpRoutesOf(httpConfig), &starling_vsopts.HttpOptions{}, rateLimit)
		expectValidEnvoyConfig(httpMan)
		return httpMan
	}
	envoyRouteOf := func(httpMan *http_connection_managerv3.HttpConnectionManager, name string) *routev3.Route {
		GinkgoHelper()
		for _, envoyRoute := range httpMan.GetRouteConfig().GetVirtualHosts()[0].GetRoutes() {
			if envoyRoute.GetName() == name {
				return envoyRoute
			}
		}
		Fail("no route " + name)
		return nil
	}
	backendRoute := func(name string) *route.Route {
		return &route.Route{
			Name:   name,
			Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/" + name}},
			Action: &route.Route_BackendUuid{BackendUuid: "backend-1"},
		}
	}

	BeforeEach(func() {
		disabledRoute := backendRoute("b")
		disabledRoute.ExternalAuth = &route.Route_ExternalAuthDisabled{ExternalAuthDisabled: true}
		cfg = &vs.HttpRoutesConfig{
			VirtualHosts: []*route.VirtualHost{{
				Domains:      []string{"a.local"},
				ExternalAuth: "sso",
				Routes:       []*route.Route{backendRoute("a"), disabledRoute},
			}},
			ExternalAuths: []*route.ExternalAuth{
				{
					Name:            "sso",
					BackendUuid:     "b-auth",
					Path:            "/check",
					Timeout:         durationpb.New(time.Second),
					RequestHeaders:  []string{"cookie"},
					UpstreamHeaders: []string{"x-auth-request-user"},
					ClientHeaders:   []string{"set-cookie"},
					StatusOnError:   503,
				},
				{
					Name:            "spare",
					BackendUuid:     "b-spare",
					Path:            "/spare",
					Timeout:         durationpb.New(time.Second),
					UpstreamHeaders: []string{"x-spare"},
					StatusOnError:   503,
				},
			},
		}
	})

	It("adds no filter when the virtual server has no policy", func() {
		Expect(extAuthzFilters(nil, httpRoutesOf(cfg))).To(BeEmpty())
	})

	It("only strips the upstream headers of every policy, even an unused one", func() {
		mutation := headerMutationOf(extAuthzFilters(cfg.GetExternalAuths(), httpRoutesOf(cfg))[0])
		Expect(removedHeaders(mutation)).To(ConsistOf("x-auth-request-user", "x-spare"))
		Expect(mutation.GetMutations().GetRequestMutations()).To(HaveLen(2))
	})

	It("sets the forwarded headers on the check request only", func() {
		authz := extAuthzOf(extAuthzFilters(cfg.GetExternalAuths(), httpRoutesOf(cfg))[1])
		Expect(headersToAdd(authz)).To(Equal(map[string]string{
			"x-forwarded-method": "%REQ(:method)%",
			"x-forwarded-host":   "%REQ(:authority)%",
			"x-forwarded-uri":    "%REQ(:path)%",
			"x-forwarded-proto":  "%REQ(:scheme)%",
			"x-forwarded-for":    "%DOWNSTREAM_REMOTE_ADDRESS_WITHOUT_PORT%",
		}))
	})

	It("renders a policy reading no request header with envoy's default allowed headers", func() {
		cfg.ExternalAuths[0].RequestHeaders = nil
		authz := extAuthzOf(extAuthzFilters(cfg.GetExternalAuths(), httpRoutesOf(cfg))[1])
		Expect(authz.GetAllowedHeaders()).To(BeNil())
		expectValidEnvoyConfig(managerOf(cfg, nil))
	})

	It("renders the policies of the rendered routes only", func() {
		Expect(filterNames(extAuthzFilters(cfg.GetExternalAuths(), nil))).To(Equal([]string{"flock.ext_authz_headers"}))
	})

	It("declares one disabled filter per used policy", func() {
		filters := extAuthzFilters(cfg.GetExternalAuths(), httpRoutesOf(cfg))
		Expect(filterNames(filters)).To(Equal([]string{"flock.ext_authz_headers", "flock.ext_authz.sso"}))
		Expect(filters[0].GetDisabled()).To(BeFalse())
		Expect(filters[1].GetDisabled()).To(BeTrue())
		authz := extAuthzOf(filters[1])
		Expect(authz.GetHttpService().GetServerUri().GetCluster()).To(Equal("b-auth"))
		Expect(authz.GetHttpService().GetServerUri().GetUri()).To(Equal("http://b-auth"))
		Expect(authz.GetHttpService().GetServerUri().GetTimeout().AsDuration()).To(Equal(time.Second))
		Expect(authz.GetHttpService().GetPathOverride()).To(Equal("/check"))
		Expect(authz.GetHttpService().GetPathPrefix()).To(BeEmpty())
		Expect(authz.GetStatusOnError().GetCode()).To(BeEquivalentTo(503))
		Expect(authz.GetFailureModeAllow()).To(BeFalse())
		Expect(authz.GetWithRequestBody()).To(BeNil())
		Expect(exactPatterns(authz.GetHttpService().GetAuthorizationResponse().GetAllowedUpstreamHeaders())).
			To(ConsistOf("x-auth-request-user"))
		Expect(exactPatterns(authz.GetHttpService().GetAuthorizationResponse().GetAllowedClientHeaders())).
			To(ConsistOf("set-cookie", "location", "www-authenticate"))
		Expect(exactPatterns(authz.GetAllowedHeaders())).To(ConsistOf("cookie"))
	})

	It("lets the request through on an error when the failure mode is ALLOW", func() {
		cfg.ExternalAuths[0].FailureMode = route.FailureMode_ALLOW
		cfg.ExternalAuths[0].StatusOnError = 401
		authz := extAuthzOf(extAuthzFilters(cfg.GetExternalAuths(), httpRoutesOf(cfg))[1])
		Expect(authz.GetFailureModeAllow()).To(BeTrue())
		Expect(authz.GetStatusOnError().GetCode()).To(BeEquivalentTo(401))
	})

	It("keeps the allowed client headers set when client_headers is empty", func() {
		cfg.ExternalAuths[0].ClientHeaders = nil
		cfg.ExternalAuths[0].UpstreamHeaders = nil
		authz := extAuthzOf(extAuthzFilters(cfg.GetExternalAuths(), httpRoutesOf(cfg))[1])
		Expect(exactPatterns(authz.GetHttpService().GetAuthorizationResponse().GetAllowedClientHeaders())).
			To(ConsistOf("location", "www-authenticate"))
		Expect(authz.GetHttpService().GetAuthorizationResponse().GetAllowedUpstreamHeaders()).To(BeNil())
	})

	It("renders no ext_authz filter for a policy named by a virtual host without routes", func() {
		cfg.VirtualHosts[0].Routes = nil
		Expect(filterNames(extAuthzFilters(cfg.GetExternalAuths(), httpRoutesOf(cfg)))).To(Equal([]string{"flock.ext_authz_headers"}))
	})

	It("keeps a policy named headers apart from the header mutation filter", func() {
		cfg.ExternalAuths[0].Name = "headers"
		cfg.VirtualHosts[0].ExternalAuth = "headers"
		httpMan := managerOf(cfg, nil)
		Expect(filterNames(httpMan.GetHttpFilters())).To(Equal([]string{"flock.ext_authz_headers",
			"flock.ext_authz.headers", "envoy.filters.http.router"}))
		perFilter := envoyRouteOf(httpMan, "a").GetTypedPerFilterConfig()
		Expect(perFilter).To(HaveKey("flock.ext_authz.headers"))
		Expect(perFilter).ToNot(HaveKey("flock.ext_authz_headers"))
		unmarshalAny(perFilter["flock.ext_authz.headers"], &ext_authzv3.ExtAuthzPerRoute{})
	})

	It("keeps the order of external_auths for the used policies", func() {
		sparePicker := backendRoute("c")
		sparePicker.ExternalAuth = &route.Route_ExternalAuthName{ExternalAuthName: "spare"}
		cfg.VirtualHosts[0].Routes = append(cfg.VirtualHosts[0].Routes, sparePicker)
		cfg.ExternalAuths[0], cfg.ExternalAuths[1] = cfg.ExternalAuths[1], cfg.ExternalAuths[0]
		Expect(filterNames(extAuthzFilters(cfg.GetExternalAuths(), httpRoutesOf(cfg)))).To(Equal([]string{"flock.ext_authz_headers",
			"flock.ext_authz.spare", "flock.ext_authz.sso"}))
	})

	It("enables the policy a route names instead of the virtual host default", func() {
		sparePicker := backendRoute("c")
		sparePicker.ExternalAuth = &route.Route_ExternalAuthName{ExternalAuthName: "spare"}
		cfg.VirtualHosts[0].Routes = append(cfg.VirtualHosts[0].Routes, sparePicker)
		perFilter := envoyRouteOf(managerOf(cfg, nil), "c").GetTypedPerFilterConfig()
		Expect(perFilter).To(HaveKey("flock.ext_authz.spare"))
		Expect(perFilter).ToNot(HaveKey("flock.ext_authz.sso"))
	})

	It("enables the policy on the routes that use it only", func() {
		httpMan := managerOf(cfg, nil)
		routeA := envoyRouteOf(httpMan, "a")
		routeB := envoyRouteOf(httpMan, "b")
		Expect(routeA.GetTypedPerFilterConfig()).To(HaveKey("flock.ext_authz.sso"))
		perRoute := unmarshalAny(routeA.GetTypedPerFilterConfig()["flock.ext_authz.sso"], &ext_authzv3.ExtAuthzPerRoute{})
		Expect(perRoute.GetCheckSettings()).ToNot(BeNil())
		Expect(routeB.GetTypedPerFilterConfig()).ToNot(HaveKey("flock.ext_authz.sso"))
	})

	It("keeps both the WAF and the policy of a route", func() {
		routeOpts, err := anypb.New(&starling_route.VirtualHostRouteOptions{Waf: &starling_route.Waf{}})
		Expect(err).ToNot(HaveOccurred())
		cfg.VirtualHosts[0].Routes[0].TypedOptions = routeOpts

		envoyRoute := envoyRouteOf(managerOf(cfg, nil), "a")
		Expect(envoyRoute.GetTypedPerFilterConfig()).To(HaveKey(wafFilterName))
		Expect(envoyRoute.GetTypedPerFilterConfig()).To(HaveKey("flock.ext_authz.sso"))
	})

	It("orders rate limit, auth, WAF and router", func() {
		routeOpts, err := anypb.New(&starling_route.VirtualHostRouteOptions{Waf: &starling_route.Waf{}})
		Expect(err).ToNot(HaveOccurred())
		cfg.VirtualHosts[0].Routes[0].TypedOptions = routeOpts
		rateLimit := &starling_limit.StarlingRateLimit{Descriptors: []*starling_limit.Descriptor{
			{Key: "remote_address", Policy: &starling_limit.Policy{Unit: flocktype.TimeUnit_MINUTE, RequestsPerUnit: 10}},
		}}

		httpMan := managerOf(cfg, rateLimit)
		Expect(filterNames(httpMan.GetHttpFilters())).To(Equal([]string{"envoy.filters.http.local_ratelimit",
			"flock.ext_authz_headers", "flock.ext_authz.sso", wafFilterName, "envoy.filters.http.router"}))
	})
})
