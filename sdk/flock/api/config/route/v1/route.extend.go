package route

import (
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

func (x *VirtualHost) GetCertificatesUuids() []string {
	if x == nil {
		return []string{}
	}
	uuids := make([]string, 0)
	for _, route := range x.GetTlsOptions() {
		if len(route.GetCertificatesUuid()) == 0 {
			continue
		}
		uuids = append(uuids, route.GetCertificatesUuid()...)
	}
	return uuids
}
func (x *VirtualHost) GetBackendUuids() []string {
	if x == nil {
		return []string{}
	}
	uuids := make([]string, 0)
	for _, route := range x.GetRoutes() {
		if route.GetBackendUuid() == "" {
			continue
		}
		uuids = append(uuids, route.GetBackendUuid())
	}
	return uuids
}

func (x *VirtualHost) ForeignResources() map[string][]string {
	return map[string][]string{
		helpers.TypeUrl[*cert.Certificate](): x.GetCertificatesUuids(),
	}
}
func (x *VirtualHost) AddMetadata(key, value string) {
	if x.Metadata == nil {
		x.Metadata = make(map[string]string)
	}
	x.Metadata[key] = value
}

func (x *VirtualHost) DeleteMetadata(key string) map[string]string {
	if x.Metadata == nil {
		return nil
	}
	delete(x.Metadata, key)
	return x.Metadata
}

func (x *VirtualHost) SetMetadata(m map[string]string) {
	x.Metadata = m
}

func (x *VirtualHost) ProvDefault() {
	if x == nil {
		return
	}
	if len(x.GetDomains()) == 0 {
		x.Domains = []string{"*"}
	}

}

func (x *Route) GetStarlingOptions() (*starling_route.VirtualHostRouteOptions, error) {
	options := &starling_route.VirtualHostRouteOptions{}
	if x.GetTypedOptions() == nil {
		return options, nil
	}
	err := x.GetTypedOptions().UnmarshalTo(options)
	if err != nil {
		return options, err
	}
	return options, nil
}
