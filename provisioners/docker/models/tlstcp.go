package models

import (
	"fmt"
	"slices"

	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"google.golang.org/protobuf/proto"
)

// TlsTcpRoute sends to backendUuid the connections whose SNI is one of the domains of the container, decrypted with
// certUuid, or still encrypted with io.seagalia.flock.tls.passthrough.
func (c *Converter) TlsTcpRoute(b *Base, backendUuid, certUuid string) (*vs.TlsTcpRouteConfig, error) {
	if b.AuthPolicy() != "" {
		return nil, fmt.Errorf("%s: %sauth.policy is for http and http2 only", b.Name, LabelPrefix)
	}
	tlsRoute := &vs.TlsTcpRouteConfig{BackendUuid: backendUuid}
	if b.isPassthrough() {
		tlsRoute.Mode = &vs.TlsTcpRouteConfig_Passthrough{Passthrough: &vs.TlsPassthrough{ServerNames: serverNames(c.domains(b))}}
		return tlsRoute, nil
	}
	if certUuid == "" {
		return nil, fmt.Errorf("%s: TLS termination needs a certificate", b.Name)
	}
	tlsRoute.Mode = &vs.TlsTcpRouteConfig_Tls{Tls: &core.TLS{
		ServerNames:      serverNames(c.domains(b)),
		CertificatesUuid: []string{certUuid},
	}}
	return tlsRoute, nil
}

// TlsTcpVirtualServerRef is the uuid or name of the TLS virtual server a tcp container with TLS joins: the one of the
// label, else the one of its listen port, shared by every container with TLS on that port.
func (c *Converter) TlsTcpVirtualServerRef(b *Base) (string, error) {
	if b.VirtualServer != nil {
		return *b.VirtualServer, nil
	}
	if b.ListenPort == nil {
		return "", fmt.Errorf("%s: %slisten_port is required for tcp", b.Name, LabelPrefix)
	}
	return fmt.Sprintf("%stls-%d", NamePrefix, *b.ListenPort), nil
}

// DefaultTlsTcpVirtualServer builds the TLS virtual server of the listen port around its first route, for the caller
// that found none to join. A virtual server named by io.seagalia.flock.virtual_server is never created.
func (c *Converter) DefaultTlsTcpVirtualServer(b *Base, tlsRoute *vs.TlsTcpRouteConfig) (*vs.VirtualServer, error) {
	if b.VirtualServer != nil {
		return nil, fmt.Errorf("virtual server %s doesn't exist", *b.VirtualServer)
	}
	name, err := c.TlsTcpVirtualServerRef(b)
	if err != nil {
		return nil, err
	}
	return &vs.VirtualServer{
		Name: name,
		Port: uint32(*b.ListenPort),
		RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{
			Routes: []*vs.TlsTcpRouteConfig{tlsRoute},
		}},
		Metadata: c.metadata(),
	}, nil
}

// JoinTlsTcpRoute answers a copy of stored holding tlsRoute. A TLS route has no name: the route of the same backend is
// the one of the same container, replaced.
func JoinTlsTcpRoute(stored *vs.VirtualServer, tlsRoute *vs.TlsTcpRouteConfig) (*vs.VirtualServer, error) {
	joined := proto.CloneOf(stored)
	tlsConfig := joined.GetTlsTcpRoutesConfig()
	if tlsConfig == nil {
		return nil, fmt.Errorf("virtual server %s is not TLS over tcp, it can't take a TLS route", stored.GetName())
	}
	index := slices.IndexFunc(tlsConfig.GetRoutes(), func(existing *vs.TlsTcpRouteConfig) bool {
		return existing.GetBackendUuid() == tlsRoute.GetBackendUuid()
	})
	if index < 0 {
		tlsConfig.Routes = append(tlsConfig.Routes, proto.CloneOf(tlsRoute))
		return joined, nil
	}
	tlsConfig.Routes[index] = proto.CloneOf(tlsRoute)
	return joined, nil
}

// RemoveTlsTcpRoutes answers a copy of stored without the routes to backendUuid. A virtual server left with no route is
// invalid: the caller deletes it instead.
func RemoveTlsTcpRoutes(stored *vs.VirtualServer, backendUuid string) *vs.VirtualServer {
	cleaned := proto.CloneOf(stored)
	tlsConfig := cleaned.GetTlsTcpRoutesConfig()
	if tlsConfig == nil {
		return cleaned
	}
	tlsConfig.Routes = slices.DeleteFunc(tlsConfig.Routes, func(r *vs.TlsTcpRouteConfig) bool {
		return r.GetBackendUuid() == backendUuid
	})
	return cleaned
}
