package envoyctl

import (
	"context"
	"encoding/json"
	"strconv"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/logging"
	"google.golang.org/protobuf/encoding/protojson"
)

type ServerCallbacks struct {
	entry zerolog.Logger
}

func NewServerCallbacks() *ServerCallbacks {
	return &ServerCallbacks{
		entry: logging.Component("envoy_control_plane_callbacks"),
	}
}

func (s ServerCallbacks) OnDeltaStreamOpen(ctx context.Context, i int64, s2 string) error {
	return nil
}

func (s ServerCallbacks) OnDeltaStreamClosed(i int64, nd *corev3.Node) {
}

func (s ServerCallbacks) OnStreamDeltaRequest(i int64, request *discoverygrpc.DeltaDiscoveryRequest) error {
	if zerolog.GlobalLevel() > zerolog.DebugLevel {
		return nil
	}

	b, _ := protojson.MarshalOptions{
		AllowPartial:    true,
		EmitUnpopulated: false,
	}.Marshal(&cache.DeltaRequest{
		TypeUrl:                     request.GetTypeUrl(),
		ResourceNamesSubscribe:      request.GetResourceNamesSubscribe(),
		ResourceNamesUnsubscribe:    request.GetResourceNamesUnsubscribe(),
		ResourceLocatorsSubscribe:   request.GetResourceLocatorsSubscribe(),
		ResourceLocatorsUnsubscribe: request.GetResourceLocatorsUnsubscribe(),
		InitialResourceVersions:     request.GetInitialResourceVersions(),
		ResponseNonce:               request.GetResponseNonce(),
		ErrorDetail:                 request.GetErrorDetail(),
	})

	var nested map[string]any
	err := json.Unmarshal(b, &nested)
	if err != nil {
		s.entry.Error().Err(err).Msg("Failed to unmarshal delta request")
		return nil
	}
	entry := s.entry.With().Str("type", request.TypeUrl).Logger()
	for k, v := range flattenFields(nested) {
		entry = entry.With().Interface(k, v).Logger()
	}
	entry.Debug().Msg("Request receive")
	return nil

}

// OnStreamDeltaResponse logs the raw response at trace level only, because it carries the header values, secret ones
// included.
func (s ServerCallbacks) OnStreamDeltaResponse(i int64, request *discoverygrpc.DeltaDiscoveryRequest, response *discoverygrpc.DeltaDiscoveryResponse) {
	if zerolog.GlobalLevel() > zerolog.TraceLevel {
		return
	}
	b, _ := protojson.MarshalOptions{
		AllowPartial:    true,
		EmitUnpopulated: false,
	}.Marshal(response)
	var nested map[string]any
	err := json.Unmarshal(b, &nested)
	if err != nil {
		s.entry.Error().Err(err).Msg("Failed to unmarshal delta response")
		return
	}
	entry := s.entry.With().Str("type", request.TypeUrl).Logger()
	for k, v := range flattenFields(nested) {
		entry = entry.With().Interface(k, v).Logger()
	}
	entry.Trace().Msg("OnStreamDeltaResponse delta response")
}

// flattenFields turns nested json into dotted log fields, e.g. {"a": [{"b": 1}]} gives "a.0.b"
func flattenFields(nested map[string]any) map[string]any {
	flat := make(map[string]any)
	addFlatFields(flat, "", nested)
	return flat
}

func addFlatFields(flat map[string]any, prefix string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for k, v := range typed {
			addFlatFields(flat, joinFieldKey(prefix, k), v)
		}
	case []any:
		for i, v := range typed {
			addFlatFields(flat, joinFieldKey(prefix, strconv.Itoa(i)), v)
		}
	default:
		flat[prefix] = value
	}
}

func joinFieldKey(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
