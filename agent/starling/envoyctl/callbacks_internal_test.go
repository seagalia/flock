package envoyctl

import (
	"bytes"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/types/known/anypb"
)

var _ = Describe("flattenFields", func() {
	It("joins nested keys and list indexes with dots", func() {
		nested := map[string]any{
			"typeUrl": "backend",
			"errorDetail": map[string]any{
				"code":    float64(3),
				"details": []any{map[string]any{"message": "bad"}, "raw"},
			},
		}
		Expect(flattenFields(nested)).To(Equal(map[string]any{
			"typeUrl":                       "backend",
			"errorDetail.code":              float64(3),
			"errorDetail.details.0.message": "bad",
			"errorDetail.details.1":         "raw",
		}))
	})
})

var _ = Describe("ServerCallbacks responses in the logs", func() {
	routeType := helpers.TypeUrl[*routev3.RouteConfiguration]()
	logResponse := func(level zerolog.Level) string {
		GinkgoHelper()
		previous := zerolog.GlobalLevel()
		zerolog.SetGlobalLevel(level)
		DeferCleanup(zerolog.SetGlobalLevel, previous)
		packed, err := anypb.New(&routev3.RouteConfiguration{
			Name: "route-1",
			RequestHeadersToAdd: []*corev3.HeaderValueOption{{
				Header: &corev3.HeaderValue{Key: "x-token", Value: "s3cret-value"},
			}},
		})
		Expect(err).ToNot(HaveOccurred())
		response := &discovery.DeltaDiscoveryResponse{
			TypeUrl:   routeType,
			Resources: []*discovery.Resource{{Name: "route-1", Resource: packed}},
		}
		logs := &bytes.Buffer{}
		callbacks := ServerCallbacks{entry: zerolog.New(logs)}

		callbacks.OnStreamDeltaResponse(1, &discovery.DeltaDiscoveryRequest{TypeUrl: routeType}, response)
		return logs.String()
	}

	It("keeps the raw responses, header values included, out of the debug logs", func() {
		Expect(logResponse(zerolog.DebugLevel)).To(BeEmpty())
	})

	It("logs the raw responses at trace level", func() {
		Expect(logResponse(zerolog.TraceLevel)).To(ContainSubstring("s3cret-value"))
	})
})
