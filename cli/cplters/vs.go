package cplters

import (
	"context"

	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

type TcpRoute struct {
	Route   *vs.TcpRouteConfig
	Backend *backend.Backend
}

func (c *TcpRoute) ToMap() (map[string]any, error) {
	data, err := ProtoToMap(c.Route)
	if err != nil {
		return nil, err
	}
	backendData, err := ProtoToMap(c.Backend)
	if err != nil {
		return nil, err
	}
	data["backend"] = backendData
	return data, nil
}

type UdpRoute struct {
	Route   *vs.UdpRouteConfig
	Backend *backend.Backend
}

func (c *UdpRoute) ToMap() (map[string]any, error) {
	data, err := ProtoToMap(c.Route)
	if err != nil {
		return nil, err
	}
	backendData, err := ProtoToMap(c.Backend)
	if err != nil {
		return nil, err
	}
	data["backend"] = backendData
	return data, nil
}

type HttpRoute struct {
	Name        string
	Backends    []*backend.Backend
	VirtualHost *route.VirtualHost
}

func (c *HttpRoute) ToMap() (map[string]any, error) {
	data := make(map[string]any)
	backendData := make([]map[string]any, 0)
	for _, aBackend := range c.Backends {
		backendDataRow, err := ProtoToMap(aBackend)
		if err != nil {
			return nil, err
		}
		backendData = append(backendData, backendDataRow)
	}
	vhData, err := ProtoToMap(c.VirtualHost)
	if err != nil {
		return nil, err
	}

	data["name"] = c.Name
	data["backends"] = backendData
	data["virtual_host"] = vhData
	return data, nil
}

type TlsTcpRoute struct {
	Route   *vs.TlsTcpRouteConfig
	Backend *backend.Backend
}

func (c *TlsTcpRoute) ToMap() (map[string]any, error) {
	data, err := ProtoToMap(c.Route)
	if err != nil {
		return nil, err
	}
	backendData, err := ProtoToMap(c.Backend)
	if err != nil {
		return nil, err
	}
	data["backend"] = backendData
	return data, nil
}

type VirtualServerComplete struct {
	VS          *vs.VirtualServer
	TcpRoute    TcpRoute
	UdpRoute    UdpRoute
	HttpRoute   []HttpRoute
	TlsTcpRoute []TlsTcpRoute
}

func (c *VirtualServerComplete) ToMap() (map[string]any, error) {
	data, err := ProtoToMap(c.VS)
	if err != nil {
		return nil, err
	}
	switch c.VS.GetRouteConfig().(type) {
	case *vs.VirtualServer_TcpRouteConfig:
		tcpRouteData, err := c.TcpRoute.ToMap()
		if err != nil {
			return nil, err
		}
		data["tcp_route_config"] = tcpRouteData
	case *vs.VirtualServer_UdpRouteConfig:
		udpRouteData, err := c.UdpRoute.ToMap()
		if err != nil {
			return nil, err
		}
		data["udp_route_config"] = udpRouteData
	case *vs.VirtualServer_TlsTcpRoutesConfig:
		tlsTcpRoutes := make([]map[string]any, 0)
		for _, tlsTcpRoute := range c.TlsTcpRoute {
			tlsTcpRouteData, err := tlsTcpRoute.ToMap()
			if err != nil {
				return nil, err
			}
			tlsTcpRoutes = append(tlsTcpRoutes, tlsTcpRouteData)
		}
		data["tls_tcp_routes_config"] = tlsTcpRoutes
	case *vs.VirtualServer_HttpRoutesConfig:
		httpRoutes := make([]map[string]any, 0)
		for _, httpRoute := range c.HttpRoute {
			httpRouteData, err := httpRoute.ToMap()
			if err != nil {
				return nil, err
			}
			httpRoutes = append(httpRoutes, httpRouteData)
		}
		data["http_routes_config"] = httpRoutes
	}
	return data, nil
}

func (c *VirtualServerComplete) Complete(ctx context.Context, client *clients.Client) error {
	cls := make(map[string]*backend.Backend, 0)
	for _, clUuid := range c.VS.GetBackendUuids() {
		cl, err := client.Backend().ReadBackend(ctx, &svccore.UuidRequest{
			UUID: clUuid,
		})
		if err != nil {
			return err
		}
		cls[clUuid] = cl.GetBackend()
	}
	firstBackend := &backend.Backend{}
	if len(c.VS.GetBackendUuids()) > 0 {
		firstBackend = cls[c.VS.GetBackendUuids()[0]]
	}

	switch c.VS.GetRouteConfig().(type) {
	case *vs.VirtualServer_TcpRouteConfig:
		c.TcpRoute = TcpRoute{
			Route:   c.VS.GetTcpRouteConfig(),
			Backend: firstBackend,
		}
		c.TcpRoute.Route.BackendUuid = ""
	case *vs.VirtualServer_UdpRouteConfig:
		c.UdpRoute = UdpRoute{
			Route:   c.VS.GetUdpRouteConfig(),
			Backend: firstBackend,
		}
		c.UdpRoute.Route.BackendUuid = ""
	case *vs.VirtualServer_TlsTcpRoutesConfig:
		c.TlsTcpRoute = make([]TlsTcpRoute, 0)
		for _, tlsTcpRoute := range c.VS.GetTlsTcpRoutesConfig().GetRoutes() {
			backendUuid := tlsTcpRoute.GetBackendUuid()
			tlsTcpRoute.BackendUuid = ""
			c.TlsTcpRoute = append(c.TlsTcpRoute, TlsTcpRoute{
				Route:   tlsTcpRoute,
				Backend: cls[backendUuid],
			})
		}
	case *vs.VirtualServer_HttpRoutesConfig:
		c.HttpRoute = make([]HttpRoute, 0)
		for _, vhost := range c.VS.GetHttpRoutesConfig().GetVirtualHosts() {
			rte := HttpRoute{
				Name:        c.VS.GetName(),
				VirtualHost: vhost,
				Backends:    make([]*backend.Backend, 0),
			}
			for _, backendUuid := range vhost.GetBackendUuids() {
				rte.Backends = append(rte.Backends, cls[backendUuid])
			}
			c.HttpRoute = append(c.HttpRoute, rte)
		}
	}

	return nil
}
