//go:build e2e

package env

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/seagalia/flock/sdk/config"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	corev1 "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	"github.com/seagalia/flock/sdk/flock/services/permission/v1/svcpermconnect"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/types/known/durationpb"
)

// HostServerName is in the controller certificate, it is the name to verify when dialing controller from the host
const HostServerName = "localhost"

// Api is a user of the controller api, its clients call with the gRPC protocol
type Api struct {
	Conn     *connect.Client
	clients  *helpers.GrpcClients
	Zone     svczoneconnect.ZoneClient
	Instance svcinstanceconnect.InstanceClient
	VIP      svcvipconnect.VIPClient
	VS       svcvsconnect.VirtualServerClient
	Backend  svcbackendconnect.BackendClient
	Cert     svccertconnect.CertificateClient
	Secret   svcsecretconnect.SecretClient
	Perm     svcpermconnect.PermissionClient
	Lookup   svclookupconnect.LookupClient
	Login    svcloginconnect.LoginClient
}

func (a *Api) Close() {
	a.clients.Close()
}

// Anonymous dials controller over tls without any token
func (g *Controller) Anonymous(ctx context.Context) (*Api, error) {
	return g.dial(ctx)
}

// LoginAs gets a token from the basic or ldap login of the controller, then every call of the Api sends it
func (g *Controller) LoginAs(ctx context.Context, username, password string) (*Api, error) {
	anonymous, err := g.Anonymous(ctx)
	if err != nil {
		return nil, err
	}
	defer anonymous.Close()
	tok, err := anonymous.Login.GetToken(ctx, &svclogin.GetTokenRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		return nil, fmt.Errorf("login %s: %w", username, err)
	}
	return g.LoginWithBearer(ctx, tok.GetToken())
}

// LoginWithBearer sends token as the bearer of every call, whoever issued it
func (g *Controller) LoginWithBearer(ctx context.Context, token string) (*Api, error) {
	return g.dial(ctx, svclogin.TokenInterceptor(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})))
}

// LoginAsBreakGlass signs a break-glass token the way `flock controller admin-token` does, with the jwt_maker of the
// rendered config, so it works whatever the login service of the controller
func (g *Controller) LoginAsBreakGlass(ctx context.Context, reason string) (*Api, error) {
	maker := &config.JwtMaker{
		Alg:        config.JwtAlg{AlgRaw: jwt.SigningMethodRS256.Alg(), Alg: jwt.SigningMethodRS256},
		PrivateKey: string(g.certs.JwtPrivateKeyPem),
		Issuer:     JwtIssuer,
		Audience:   JwtAudience,
	}
	jwtMan, err := svclogin.NewJwtManager(maker)
	if err != nil {
		return nil, err
	}
	token, _, err := jwtMan.MakeBreakGlassToken(reason, svclogin.BreakGlassMaxTTL)
	if err != nil {
		return nil, err
	}
	return g.LoginWithBearer(ctx, token)
}

func (g *Controller) dial(ctx context.Context, interceptors ...connect.ClientInterceptor) (*Api, error) {
	clients := helpers.NewGrpcClients(g.certs.UserTLSConfig(HostServerName))
	client := clients.Client(g.Addr(ctx), interceptors...)
	return &Api{
		Conn:     client,
		clients:  clients,
		Zone:     svczoneconnect.NewZoneClient(client),
		Instance: svcinstanceconnect.NewInstanceClient(client),
		VIP:      svcvipconnect.NewVIPClient(client),
		VS:       svcvsconnect.NewVirtualServerClient(client),
		Backend:  svcbackendconnect.NewBackendClient(client),
		Cert:     svccertconnect.NewCertificateClient(client),
		Secret:   svcsecretconnect.NewSecretClient(client),
		Perm:     svcpermconnect.NewPermissionClient(client),
		Lookup:   svclookupconnect.NewLookupClient(client),
		Login:    svcloginconnect.NewLoginClient(client),
	}, nil
}

// NewZone is valid for instances of resource type "small" with the Null instantiater
func NewZone(name string) *zone.Zone {
	return &zone.Zone{
		Name:  name,
		Zone:  "z1",
		Pools: []*zone.IpPool{{Name: "default", Subnets: []*zone.Subnet{{Ip: "10.0.0.0", PrefixLength: 24}}}},
	}
}

func NewBackend(name string) *backend.Backend {
	return &backend.Backend{
		Name: name,
		HealthCheck: &corev1.HealthCheck{
			Timeout:       durationpb.New(time.Second),
			Interval:      durationpb.New(5 * time.Second),
			HealthChecker: &corev1.HealthCheck_NoHealthCheck{NoHealthCheck: &corev1.NoHealthCheck{}},
		},
		Endpoint: &backend.Backend_IpAddrGroup{IpAddrGroup: &corev1.IPAddrGroup{
			IpAddresses: []*corev1.IPAddr{{Ip: "10.0.0.10", Port: 8080}},
		}},
	}
}

func NewTcpVs(name string, port uint32, backendUuid string) *vs.VirtualServer {
	return &vs.VirtualServer{
		Name: name,
		Port: port,
		RouteConfig: &vs.VirtualServer_TcpRouteConfig{TcpRouteConfig: &vs.TcpRouteConfig{
			BackendUuid: backendUuid,
		}},
	}
}

func NewHttpVs(name string, port uint32, vhosts []*route.VirtualHost) *vs.VirtualServer {
	return &vs.VirtualServer{
		Name: name,
		Port: port,
		RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{
			VirtualHosts: vhosts,
		}},
	}
}

func NewTlsVirtualHost(certUuid string) *route.VirtualHost {
	return &route.VirtualHost{
		Domains:    []string{"vhost.e2e.local"},
		TlsOptions: []*corev1.TLS{{CertificatesUuid: []string{certUuid}}},
	}
}

func NewVip(name, ip string, vsUuids ...string) *vip.VIP {
	return &vip.VIP{
		Name:               name,
		Ip:                 ip,
		VirtualServersUuid: vsUuids,
	}
}

func NewInstance(name, zoneUuid string, vipUuids ...string) *instance.Instance {
	return &instance.Instance{
		Name:         name,
		ZoneUuid:     zoneUuid,
		ResourceType: "small",
		VipsUuid:     vipUuids,
	}
}
