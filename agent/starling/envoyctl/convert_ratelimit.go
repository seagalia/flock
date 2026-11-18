package envoyctl

import (
	"slices"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	local_ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/local_ratelimit/v3"
	http_connection_managerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	starling_limit "github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	starling_route "github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// maxDynamicDescriptors bounds the buckets of a descriptor with an empty value (one per value, e.g. per client
// address). Envoy keeps 20 by default and an evicted bucket starts full again, so a busy VIP would barely be limited.
const maxDynamicDescriptors = 10000

func localRateLimitFilter(statPrefix string, rateLimit *starling_limit.StarlingRateLimit) *http_connection_managerv3.HttpFilter {
	descriptors := rateLimitDescriptorsToEnvoy(rateLimit.GetDescriptors(), nil)
	if len(descriptors) == 0 {
		return nil
	}
	// envoy enables and enforces the filter for no request by default
	everyRequest := &corev3.RuntimeFractionalPercent{
		DefaultValue: &typev3.FractionalPercent{Numerator: 100, Denominator: typev3.FractionalPercent_HUNDRED},
	}
	localRateLimit, _ := anypb.New(&local_ratelimitv3.LocalRateLimit{ // nolint
		StatPrefix:            statPrefix,
		FilterEnabled:         everyRequest,
		FilterEnforced:        everyRequest,
		Descriptors:           descriptors,
		MaxDynamicDescriptors: wrapperspb.UInt32(maxDynamicDescriptors),
	})
	return &http_connection_managerv3.HttpFilter{
		Name: "envoy.filters.http.local_ratelimit",
		ConfigType: &http_connection_managerv3.HttpFilter_TypedConfig{
			TypedConfig: localRateLimit,
		},
	}
}

// rateLimitDescriptorsToEnvoy flattens the tree of descriptors because envoy only matches a request on the whole list
// of entries of a descriptor, from the root down to the descriptor with a policy.
func rateLimitDescriptorsToEnvoy(descriptors []*starling_limit.Descriptor, parentEntries []*ratelimitv3.RateLimitDescriptor_Entry) []*ratelimitv3.LocalRateLimitDescriptor {
	envoyDescriptors := make([]*ratelimitv3.LocalRateLimitDescriptor, 0)
	for _, descriptor := range descriptors {
		entries := append(slices.Clone(parentEntries), &ratelimitv3.RateLimitDescriptor_Entry{
			Key:   descriptor.GetKey(),
			Value: descriptor.GetValue(),
		})
		if descriptor.GetPolicy() != nil {
			envoyDescriptors = append(envoyDescriptors, &ratelimitv3.LocalRateLimitDescriptor{
				Entries:     entries,
				TokenBucket: policyToTokenBucket(descriptor.GetPolicy()),
				ShadowMode:  descriptor.GetShadowMode(),
			})
		}
		envoyDescriptors = append(envoyDescriptors, rateLimitDescriptorsToEnvoy(descriptor.GetDescriptors(), entries)...)
	}
	return envoyDescriptors
}

func policyToTokenBucket(policy *starling_limit.Policy) *typev3.TokenBucket {
	fillInterval := time.Second
	switch policy.GetUnit() {
	case flocktype.TimeUnit_MINUTE:
		fillInterval = time.Minute
	case flocktype.TimeUnit_HOUR:
		fillInterval = time.Hour
	case flocktype.TimeUnit_DAY:
		fillInterval = 24 * time.Hour
	}
	return &typev3.TokenBucket{
		MaxTokens:     policy.GetRequestsPerUnit(),
		TokensPerFill: wrapperspb.UInt32(policy.GetRequestsPerUnit()),
		FillInterval:  durationpb.New(fillInterval),
	}
}

func routeRateLimitsToEnvoy(rateLimits []*starling_route.RouteRateLimit) []*routev3.RateLimit {
	envoyRateLimits := make([]*routev3.RateLimit, 0, len(rateLimits))
	for _, rateLimit := range rateLimits {
		actions := make([]*routev3.RateLimit_Action, 0)
		for _, action := range rateLimit.GetActions() {
			switch spec := action.GetActionSpecifier().(type) {
			case *starling_route.RouteRateLimit_Action_RequestHeaders_:
				actions = append(actions, &routev3.RateLimit_Action{
					ActionSpecifier: &routev3.RateLimit_Action_RequestHeaders_{
						RequestHeaders: &routev3.RateLimit_Action_RequestHeaders{
							HeaderName:    spec.RequestHeaders.GetHeaderName(),
							DescriptorKey: spec.RequestHeaders.GetDescriptorKey(),
						},
					},
				})
			case *starling_route.RouteRateLimit_Action_RemoteAddress_:
				actions = append(actions, &routev3.RateLimit_Action{
					ActionSpecifier: &routev3.RateLimit_Action_RemoteAddress_{
						RemoteAddress: &routev3.RateLimit_Action_RemoteAddress{},
					},
				})
			case *starling_route.RouteRateLimit_Action_GenericKey_:
				actions = append(actions, &routev3.RateLimit_Action{
					ActionSpecifier: &routev3.RateLimit_Action_GenericKey_{
						GenericKey: &routev3.RateLimit_Action_GenericKey{
							DescriptorValue: spec.GenericKey.GetDescriptorValue(),
							DescriptorKey:   spec.GenericKey.GetDescriptorKey(),
						},
					},
				})
			}
		}
		envoyRateLimits = append(envoyRateLimits, &routev3.RateLimit{Actions: actions})
	}
	return envoyRateLimits
}
