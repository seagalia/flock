package vs

import (
	"slices"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/limit/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

func (x *VirtualServer) Signature() (string, error) {
	return helpers.MessageSignature(x)
}

func (x *VirtualServer) GetIdentifier() string {
	return x.GetUUID()
}

func (x *VirtualServer) SetIdentifier(identifier string) {
	x.UUID = identifier
}

func (x *VirtualServer) GetBackendUuids() []string {
	if x == nil {
		return []string{}
	}
	switch x.GetRouteConfig().(type) {
	case *VirtualServer_HttpRoutesConfig:
		uuids := make([]string, 0)
		for _, vhost := range x.GetHttpRoutesConfig().GetVirtualHosts() {
			uuids = append(uuids, vhost.GetBackendUuids()...)
		}
		for _, policy := range x.GetHttpRoutesConfig().GetExternalAuths() {
			if slices.Contains(uuids, policy.GetBackendUuid()) {
				continue
			}
			uuids = append(uuids, policy.GetBackendUuid())
		}
		return uuids
	case *VirtualServer_TlsTcpRoutesConfig:
		uuids := make([]string, 0)
		for _, route := range x.GetTlsTcpRoutesConfig().GetRoutes() {
			uuids = append(uuids, route.GetBackendUuid())
		}
		return uuids
	case *VirtualServer_TcpRouteConfig:
		return []string{
			x.GetTcpRouteConfig().GetBackendUuid(),
		}
	case *VirtualServer_UdpRouteConfig:
		return []string{
			x.GetUdpRouteConfig().GetBackendUuid(),
		}
	}
	return []string{}
}

func (x *VirtualServer) ProvDefault() {
	for _, policy := range x.GetHttpRoutesConfig().GetExternalAuths() {
		policy.ProvDefault()
	}
}

func (x *VirtualServer) GetCertificatesUuids() []string {
	if x == nil {
		return []string{}
	}
	uuids := make([]string, 0)
	if routes, ok := x.GetRouteConfig().(*VirtualServer_TlsTcpRoutesConfig); ok {
		for _, route := range routes.TlsTcpRoutesConfig.GetRoutes() {
			if route.GetTls() == nil && len(route.GetTls().GetCertificatesUuid()) == 0 {
				continue
			}
			uuids = append(uuids, route.GetTls().GetCertificatesUuid()...)
		}
		return uuids
	}
	if routes, ok := x.GetRouteConfig().(*VirtualServer_HttpRoutesConfig); ok {
		for _, vhost := range routes.HttpRoutesConfig.GetVirtualHosts() {
			uuids = append(uuids, vhost.GetCertificatesUuids()...)
		}
	}
	return uuids
}

func (x *VirtualServer) RetrieveRouteConfig() IsRouteConfig {
	if x == nil {
		return nil
	}
	switch x.GetRouteConfig().(type) {
	case *VirtualServer_TlsTcpRoutesConfig:
		return x.GetTlsTcpRoutesConfig()
	case *VirtualServer_HttpRoutesConfig:
		return x.GetHttpRoutesConfig()
	case *VirtualServer_TcpRouteConfig:
		return x.GetTcpRouteConfig()
	case *VirtualServer_UdpRouteConfig:
		return x.GetUdpRouteConfig()
	}
	return nil
}
func (x *VirtualServer) ForeignResources() map[string][]string {
	return map[string][]string{
		helpers.TypeUrl[*cert.Certificate](): x.GetCertificatesUuids(),
		helpers.TypeUrl[*backend.Backend]():  x.GetBackendUuids(),
	}
}
func (x *VirtualServer) AddMetadata(key, value string) {
	if x.Metadata == nil {
		x.Metadata = make(map[string]string)
	}
	x.Metadata[key] = value
}

func (x *VirtualServer) DeleteMetadata(key string) map[string]string {
	if x.Metadata == nil {
		return nil
	}
	delete(x.Metadata, key)
	return x.Metadata
}

func (x *VirtualServer) SetMetadata(m map[string]string) {
	x.Metadata = m
}

func (x *VirtualServer) GetFirstConnLimit() *limit.ConnectionLimit {
	if x.GetLimitConfig() == nil {
		return nil
	}
	for _, lim := range x.GetLimitConfig() {
		if lim.GetConnectionLimit() != nil {
			return lim.GetConnectionLimit()
		}
	}
	return nil
}

func (x *VirtualServer) GetFirstConnRateLimit() *limit.ConnectionRateLimit {
	if x.GetLimitConfig() == nil {
		return nil
	}
	for _, lim := range x.GetLimitConfig() {
		if lim.GetConnectionRateLimit() != nil {
			return lim.GetConnectionRateLimit()
		}
	}
	return nil
}

func (x *VirtualServer) GetStarlingRateLimit() *starling_limit.StarlingRateLimit {
	if x.GetLimitConfig() == nil {
		return nil
	}
	opts := &starling_limit.StarlingRateLimit{}
	for _, lim := range x.GetLimitConfig() {
		if lim.GetTypedLimit() == nil {
			continue
		}
		err := lim.GetTypedLimit().UnmarshalTo(opts)
		if err != nil {
			continue
		}
		return opts
	}
	return nil
}
