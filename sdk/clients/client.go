package clients

import (
	"connectrpc.com/connect/v2"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	"github.com/seagalia/flock/sdk/flock/services/core/v1/svccoreconnect"
	"github.com/seagalia/flock/sdk/flock/services/discovery/v1/svcdiscoveryconnect"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	"github.com/seagalia/flock/sdk/flock/services/permission/v1/svcpermconnect"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
)

type Client struct {
	client         *connect.Client
	backendClient  svcbackendconnect.BackendClient
	instanceClient svcinstanceconnect.InstanceClient
	vipClient      svcvipconnect.VIPClient
	vsClient       svcvsconnect.VirtualServerClient
	loginClient    svcloginconnect.LoginClient
	discoClient    svcdiscoveryconnect.AggregatedDiscoveryClient
	infoClient     svccoreconnect.InfoClient
	permClient     svcpermconnect.PermissionClient
	lookupClient   svclookupconnect.LookupClient
	certClient     svccertconnect.CertificateClient
	zoneClient     svczoneconnect.ZoneClient
	secretClient   svcsecretconnect.SecretClient
}

// New gives the clients of every controller service, build client with helpers.GrpcClients
func New(conn *connect.Client) *Client {
	return &Client{
		client:         conn,
		backendClient:  svcbackendconnect.NewBackendClient(conn),
		instanceClient: svcinstanceconnect.NewInstanceClient(conn),
		vipClient:      svcvipconnect.NewVIPClient(conn),
		vsClient:       svcvsconnect.NewVirtualServerClient(conn),
		loginClient:    svcloginconnect.NewLoginClient(conn),
		discoClient:    svcdiscoveryconnect.NewAggregatedDiscoveryClient(conn),
		infoClient:     svccoreconnect.NewInfoClient(conn),
		permClient:     svcpermconnect.NewPermissionClient(conn),
		lookupClient:   svclookupconnect.NewLookupClient(conn),
		certClient:     svccertconnect.NewCertificateClient(conn),
		zoneClient:     svczoneconnect.NewZoneClient(conn),
		secretClient:   svcsecretconnect.NewSecretClient(conn),
	}
}

func (c *Client) Conn() *connect.Client {
	return c.client
}

func (c *Client) Backend() svcbackendconnect.BackendClient {
	return c.backendClient
}

func (c *Client) Instance() svcinstanceconnect.InstanceClient {
	return c.instanceClient
}

func (c *Client) Vip() svcvipconnect.VIPClient {
	return c.vipClient
}

func (c *Client) VirtualServer() svcvsconnect.VirtualServerClient {
	return c.vsClient
}

func (c *Client) Login() svcloginconnect.LoginClient {
	return c.loginClient
}

func (c *Client) ADS() svcdiscoveryconnect.AggregatedDiscoveryClient {
	return c.discoClient
}

func (c *Client) Info() svccoreconnect.InfoClient {
	return c.infoClient
}

func (c *Client) Permission() svcpermconnect.PermissionClient {
	return c.permClient
}

func (c *Client) Lookup() svclookupconnect.LookupClient {
	return c.lookupClient
}

func (c *Client) Certificate() svccertconnect.CertificateClient {
	return c.certClient
}

func (c *Client) Zone() svczoneconnect.ZoneClient {
	return c.zoneClient
}

func (c *Client) Secret() svcsecretconnect.SecretClient {
	return c.secretClient
}
