package envoyctl

import (
	"context"
	"sync/atomic"

	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// RawDeltaResponse is a pre-serialized xDS response that utilizes the delta discovery request/response objects.
type RawDeltaResponse struct {
	// DeltaRequest is the latest delta request on the stream.
	DeltaRequest *discovery.DeltaDiscoveryRequest

	// SystemVersionInfo holds the currently applied response system version and should be used for debugging purposes only.
	SystemVersionInfo string

	// Resources to be included in the response.
	Resource proto.Message

	Signature string

	// RemovedResources is a list of resource aliases which should be dropped by the consuming client.
	RemovedResources []string

	// NextVersionMap consists of updated version mappings after this response is applied
	NextVersionMap map[string]string

	// Context provided at the time of response creation. This allows associating additional
	// information with a generated response.
	Ctx context.Context

	// Marshaled Resources to be included in the response.
	marshaledResponse atomic.Value
}

// GetDeltaDiscoveryResponse marshals once and caches the result, marshaling is costly under load.
func (r *RawDeltaResponse) GetDeltaDiscoveryResponse() (*discovery.DeltaDiscoveryResponse, error) {
	marshaledResponse := r.marshaledResponse.Load()

	if marshaledResponse == nil {
		var resources []*discovery.Resource
		if r.Resource != nil {
			name := cache.GetResourceName(r.Resource)
			res, err := anypb.New(r.Resource)
			if err != nil {
				return nil, err
			}
			resources = []*discovery.Resource{
				{
					Name:     name,
					Resource: res,
					Version:  r.Signature,
				},
			}
		}

		marshaledResponse = &discovery.DeltaDiscoveryResponse{
			Resources:         resources,
			RemovedResources:  r.RemovedResources,
			TypeUrl:           r.DeltaRequest.TypeUrl,
			SystemVersionInfo: r.SystemVersionInfo,
		}
		r.marshaledResponse.Store(marshaledResponse)
	}

	return marshaledResponse.(*discovery.DeltaDiscoveryResponse), nil
}

// GetDeltaRequest returns the original DeltaRequest
func (r *RawDeltaResponse) GetDeltaRequest() *discovery.DeltaDiscoveryRequest {
	return r.DeltaRequest
}

// GetSystemVersion returns the raw SystemVersion
func (r *RawDeltaResponse) GetSystemVersion() (string, error) {
	return r.SystemVersionInfo, nil
}

func (r *RawDeltaResponse) GetResponseVersion() string {
	return r.SystemVersionInfo
}

// GetNextVersionMap returns the version mappings updated after this response is applied
func (r *RawDeltaResponse) GetNextVersionMap() map[string]string {
	return r.NextVersionMap
}

func (r *RawDeltaResponse) GetReturnedResources() map[string]string {
	return r.NextVersionMap
}

func (r *RawDeltaResponse) GetContext() context.Context {
	return r.Ctx
}
