package envoyctl

import (
	"time"

	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	local_ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/local_ratelimit/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	starling_limit "github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	starling_route "github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

var _ = Describe("Rate limit conversion", func() {
	perMinute := func(requests uint32) *starling_limit.Policy {
		return &starling_limit.Policy{Unit: flocktype.TimeUnit_MINUTE, RequestsPerUnit: requests}
	}
	entry := func(key, value string) *ratelimitv3.RateLimitDescriptor_Entry {
		return &ratelimitv3.RateLimitDescriptor_Entry{Key: key, Value: value}
	}

	Context("localRateLimitFilter", func() {
		It("should give no filter without descriptors", func() {
			Expect(localRateLimitFilter("vs-uuid", nil)).To(BeNil())
			Expect(localRateLimitFilter("vs-uuid", &starling_limit.StarlingRateLimit{})).To(BeNil())
		})

		It("should give no filter when no descriptor has a policy", func() {
			rateLimit := &starling_limit.StarlingRateLimit{Descriptors: []*starling_limit.Descriptor{{Key: "remote_address"}}}
			Expect(localRateLimitFilter("vs-uuid", rateLimit)).To(BeNil())
		})

		It("should enable and enforce the limits on every request", func() {
			rateLimit := &starling_limit.StarlingRateLimit{Descriptors: []*starling_limit.Descriptor{
				{Key: "remote_address", Policy: perMinute(10)},
			}}
			filter := localRateLimitFilter("vs-uuid", rateLimit)
			Expect(filter.GetName()).To(Equal("envoy.filters.http.local_ratelimit"))

			conf := unmarshalAny(filter.GetTypedConfig(), &local_ratelimitv3.LocalRateLimit{})
			Expect(conf.GetStatPrefix()).To(Equal("vs-uuid"))
			Expect(conf.GetFilterEnabled().GetDefaultValue().GetNumerator()).To(BeEquivalentTo(100))
			Expect(conf.GetFilterEnforced().GetDefaultValue().GetNumerator()).To(BeEquivalentTo(100))
			Expect(conf.GetTokenBucket()).To(BeNil())
			Expect(conf.GetMaxDynamicDescriptors().GetValue()).To(BeEquivalentTo(maxDynamicDescriptors))
			Expect(conf.GetDescriptors()).To(HaveLen(1))
		})
	})

	Context("rateLimitDescriptorsToEnvoy", func() {
		It("should give one descriptor per policy with the entries from the root", func() {
			descriptors := rateLimitDescriptorsToEnvoy([]*starling_limit.Descriptor{
				{Key: "remote_address", Policy: perMinute(100), Descriptors: []*starling_limit.Descriptor{
					{Key: "user", Value: "alice", Policy: perMinute(10), ShadowMode: true},
				}},
				{Key: "generic_key", Value: "api", Descriptors: []*starling_limit.Descriptor{
					{Key: "user", Policy: perMinute(5)},
				}},
			}, nil)

			Expect(descriptors).To(HaveLen(3))
			Expect(descriptors[0].GetEntries()).To(Equal([]*ratelimitv3.RateLimitDescriptor_Entry{entry("remote_address", "")}))
			Expect(descriptors[0].GetTokenBucket().GetMaxTokens()).To(BeEquivalentTo(100))
			Expect(descriptors[0].GetShadowMode()).To(BeFalse())

			Expect(descriptors[1].GetEntries()).To(Equal([]*ratelimitv3.RateLimitDescriptor_Entry{
				entry("remote_address", ""), entry("user", "alice"),
			}))
			Expect(descriptors[1].GetTokenBucket().GetMaxTokens()).To(BeEquivalentTo(10))
			Expect(descriptors[1].GetShadowMode()).To(BeTrue())

			Expect(descriptors[2].GetEntries()).To(Equal([]*ratelimitv3.RateLimitDescriptor_Entry{
				entry("generic_key", "api"), entry("user", ""),
			}))
			for _, descriptor := range descriptors {
				expectValidEnvoyConfig(descriptor)
			}
		})

		It("should not share the entries of a parent between its sub descriptors", func() {
			descriptors := rateLimitDescriptorsToEnvoy([]*starling_limit.Descriptor{
				{Key: "remote_address", Descriptors: []*starling_limit.Descriptor{
					{Key: "user", Value: "alice", Policy: perMinute(1)},
					{Key: "user", Value: "bob", Policy: perMinute(1)},
				}},
			}, nil)

			Expect(descriptors).To(HaveLen(2))
			Expect(descriptors[0].GetEntries()[1].GetValue()).To(Equal("alice"))
			Expect(descriptors[1].GetEntries()[1].GetValue()).To(Equal("bob"))
		})
	})

	Context("policyToTokenBucket", func() {
		DescribeTable("should refill the whole bucket once per unit",
			func(unit flocktype.TimeUnit, expected time.Duration) {
				bucket := policyToTokenBucket(&starling_limit.Policy{Unit: unit, RequestsPerUnit: 30})
				expectValidEnvoyConfig(bucket)
				Expect(bucket.GetMaxTokens()).To(BeEquivalentTo(30))
				Expect(bucket.GetTokensPerFill().GetValue()).To(BeEquivalentTo(30))
				Expect(bucket.GetFillInterval().AsDuration()).To(Equal(expected))
			},
			Entry("unset is a second", flocktype.TimeUnit_UNKNOWN, time.Second),
			Entry("second", flocktype.TimeUnit_SECOND, time.Second),
			Entry("minute", flocktype.TimeUnit_MINUTE, time.Minute),
			Entry("hour", flocktype.TimeUnit_HOUR, time.Hour),
			Entry("day", flocktype.TimeUnit_DAY, 24*time.Hour),
		)
	})

	Context("routeRateLimitsToEnvoy", func() {
		It("should keep the actions of each rate limit in order", func() {
			rateLimits := routeRateLimitsToEnvoy([]*starling_route.RouteRateLimit{
				{Actions: []*starling_route.RouteRateLimit_Action{
					{ActionSpecifier: &starling_route.RouteRateLimit_Action_RemoteAddress_{
						RemoteAddress: &starling_route.RouteRateLimit_Action_RemoteAddress{},
					}},
					{ActionSpecifier: &starling_route.RouteRateLimit_Action_RequestHeaders_{
						RequestHeaders: &starling_route.RouteRateLimit_Action_RequestHeaders{HeaderName: "x-user", DescriptorKey: "user"},
					}},
				}},
				{Actions: []*starling_route.RouteRateLimit_Action{
					{ActionSpecifier: &starling_route.RouteRateLimit_Action_GenericKey_{
						GenericKey: &starling_route.RouteRateLimit_Action_GenericKey{DescriptorValue: "api", DescriptorKey: "zone"},
					}},
				}},
			})

			Expect(rateLimits).To(HaveLen(2))
			for _, rateLimit := range rateLimits {
				expectValidEnvoyConfig(rateLimit)
			}
			first := rateLimits[0].GetActions()
			Expect(first).To(HaveLen(2))
			Expect(first[0].GetRemoteAddress()).ToNot(BeNil())
			Expect(first[1].GetRequestHeaders().GetHeaderName()).To(Equal("x-user"))
			Expect(first[1].GetRequestHeaders().GetDescriptorKey()).To(Equal("user"))
			second := rateLimits[1].GetActions()
			Expect(second[0].GetGenericKey().GetDescriptorValue()).To(Equal("api"))
			Expect(second[0].GetGenericKey().GetDescriptorKey()).To(Equal("zone"))
		})
	})

	Context("in the http manager", func() {
		var routes []*HttpRoute

		BeforeEach(func() {
			routeOpts, err := anypb.New(&starling_route.VirtualHostRouteOptions{RateLimits: []*starling_route.RouteRateLimit{
				{Actions: []*starling_route.RouteRateLimit_Action{{ActionSpecifier: &starling_route.RouteRateLimit_Action_RemoteAddress_{
					RemoteAddress: &starling_route.RouteRateLimit_Action_RemoteAddress{},
				}}}},
			}})
			Expect(err).ToNot(HaveOccurred())
			routes = []*HttpRoute{{
				Name: "vs-0",
				VirtualHost: &route.VirtualHost{Domains: []string{"a.local"}, Routes: []*route.Route{
					{
						Name:         "limited",
						Match:        &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
						Action:       &route.Route_BackendUuid{BackendUuid: "backend-1"},
						TypedOptions: routeOpts,
					},
					{
						Name:         "redirect",
						Match:        &route.RouteMatch{PathSpecifier: &route.RouteMatch_Path{Path: "/old"}},
						Action:       &route.Route_Redirect{Redirect: &route.RedirectAction{PathRedirect: "/new"}},
						TypedOptions: routeOpts,
					},
				}},
				Backends: []*backend.Backend{{UUID: "backend-1"}},
			}}
		})

		It("should limit before routing", func() {
			rateLimit := &starling_limit.StarlingRateLimit{Descriptors: []*starling_limit.Descriptor{
				{Key: "remote_address", Policy: perMinute(10)},
			}}
			httpMan := virtualHostToHttpManager("vs-uuid", nil, routes, &starling_vsopts.HttpOptions{}, rateLimit)
			expectValidEnvoyConfig(httpMan)

			filters := httpMan.GetHttpFilters()
			Expect(filters).To(HaveLen(2))
			Expect(filters[0].GetName()).To(Equal("envoy.filters.http.local_ratelimit"))
			Expect(filters[1].GetName()).To(Equal("envoy.filters.http.router"))
		})

		It("should give the rate limits to the routes to a backend only", func() {
			httpMan := virtualHostToHttpManager("vs-uuid", nil, routes, &starling_vsopts.HttpOptions{}, nil)
			expectValidEnvoyConfig(httpMan)

			envoyRoutes := httpMan.GetRouteConfig().GetVirtualHosts()[0].GetRoutes()
			Expect(envoyRoutes[0].GetRoute().GetRateLimits()).To(HaveLen(1))
			Expect(envoyRoutes[0].GetRoute().GetRateLimits()[0].GetActions()[0].GetActionSpecifier()).
				To(BeAssignableToTypeOf(&routev3.RateLimit_Action_RemoteAddress_{}))
			Expect(envoyRoutes[1].GetRedirect()).ToNot(BeNil())
			Expect(httpMan.GetHttpFilters()).To(HaveLen(1))
		})
	})
})
