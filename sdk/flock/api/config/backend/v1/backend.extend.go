package backend

import (
	"time"

	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"google.golang.org/protobuf/types/known/durationpb"

	starling_backendopts "github.com/seagalia/flock/sdk/flock/extensions/backend/starling_options/v1"
	starling_commopts "github.com/seagalia/flock/sdk/flock/extensions/common/starling_options/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

func (x *Backend) Signature() (string, error) {
	return helpers.MessageSignature(x)
}

func (x *Backend) GetIdentifier() string {
	return x.GetUUID()
}

func (x *Backend) SetIdentifier(identifier string) {
	x.UUID = identifier
}

func (x *Backend) GetStarlingHttpProtocolOptions() (*starling_commopts.HttpProtocolOptions, error) {
	options := &starling_commopts.HttpProtocolOptions{}
	if x.GetHttpProtocolOptions() == nil {
		return nil, nil
	}
	err := x.GetHttpProtocolOptions().UnmarshalTo(options)
	if err != nil {
		return options, err
	}
	return options, nil
}

func (x *Backend) GetStarlingOptions() (*starling_backendopts.BackendOptions, error) {
	options := &starling_backendopts.BackendOptions{}
	if x.GetTypedOptions() == nil {
		return options, nil
	}
	err := x.GetTypedOptions().UnmarshalTo(options)
	if err != nil {
		return options, err
	}
	return options, nil
}

func (x *Backend) AddMetadata(key, value string) {
	if x.Metadata == nil {
		x.Metadata = make(map[string]string)
	}
	x.Metadata[key] = value
}

func (x *Backend) DeleteMetadata(key string) map[string]string {
	if x.Metadata == nil {
		return nil
	}
	delete(x.Metadata, key)
	return x.Metadata
}

func (x *Backend) SetMetadata(m map[string]string) {
	x.Metadata = m
}

func (x *Backend) ProvDefault() {
	if x == nil {
		return
	}
	if x.GetRefreshRate().AsDuration() == 0 {
		x.RefreshRate = durationpb.New(5000 * time.Millisecond)
	}
	if x.GetHealthCheck() == nil {
		x.HealthCheck = &core.HealthCheck{}
	}
	x.GetHealthCheck().ProvDefault()
}
