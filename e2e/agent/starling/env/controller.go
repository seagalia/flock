//go:build e2e

package env

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.yaml.in/yaml/v2"
	"golang.org/x/oauth2"
)

const (
	controllerDir      = "/etc/flock"
	controllerPort     = "8443/tcp"
	controllerReadyLog = "Starting grpc and http server"

	controllerConfigPath = controllerDir + "/controller.yml"

	ControllerAdmin    = "admin"
	ControllerPassword = "admin"
)

// ControllerAgent is an agent the static instantiator of the controller declares under AgentUuid(Name), the CN of its
// certificate is Name. It listens on 18000, a provisioner on 18002.
type ControllerAgent struct {
	Name        string
	IP          string
	Provisioner bool
}

type ControllerOptions struct {
	IP           string
	Zone         string
	ResourceType string
	Agents       []ControllerAgent
	// AdminGroups are admin beside the admin user, like agent-<name> for the calls made with the certificate of the
	// agent named name
	AdminGroups []string
}

// Controller is a real controller on a sqlite store, agents dial it at Target and tests at HostAddr
type Controller struct {
	Container testcontainers.Container
	certs     *Certs
	ip        string
}

// StartController runs the image of the controller e2e suite. It dials the agents with its client certificate and
// expects AgentServerName in theirs.
func (e *Env) StartController(ctx context.Context, o ControllerOptions) (*Controller, error) {
	image, err := e.imageFrom(ctx, "e2e/controller/docker/controller.Dockerfile", "controller")
	if err != nil {
		return nil, err
	}
	confYaml, err := renderControllerConfig(o)
	if err != nil {
		return nil, err
	}
	c, err := e.start(ctx, o.IP, testcontainers.ContainerRequest{
		Image:          image,
		ExposedPorts:   []string{controllerPort},
		ConfigModifier: withHostname("controller"),
		Files: []testcontainers.ContainerFile{
			fileFromBytes(confYaml, controllerConfigPath, 0o644),
			fileFromBytes(e.Certs.CAPem, controllerDir+"/ca.pem", 0o644),
			fileFromBytes(e.Certs.ControllerCertPem, controllerDir+"/controller.pem", 0o644),
			fileFromBytes(e.Certs.ControllerKeyPem, controllerDir+"/controller-key.pem", 0o600),
			fileFromBytes(e.Certs.ControllerClientCertPem, controllerDir+"/agent-client.pem", 0o644),
			fileFromBytes(e.Certs.ControllerClientKeyPem, controllerDir+"/agent-client-key.pem", 0o600),
		},
		WaitingFor: wait.ForAll(wait.ForLog(controllerReadyLog), wait.ForListeningPort(controllerPort)),
	})
	if err != nil {
		return nil, err
	}
	return &Controller{Container: c, certs: e.Certs, ip: o.IP}, nil
}

func renderControllerConfig(o ControllerOptions) ([]byte, error) {
	jwtPrivatePem, err := jwtPrivateKey()
	if err != nil {
		return nil, err
	}
	agents := make([]map[string]any, 0, len(o.Agents))
	for _, agent := range o.Agents {
		declared := map[string]any{
			"uuid": AgentUuid(agent.Name), "name": agent.Name, "host": agent.IP + ":18000",
			"features": map[string]any{"show_logs": true, "show_status": true, "show_metrics": true},
		}
		if agent.Provisioner {
			declared["host"] = agent.IP + ":18002"
			declared["type"] = "provisioner"
			declared["mode"] = "PROVISIONER"
		}
		agents = append(agents, declared)
	}
	conf := map[string]any{
		"log": map[string]any{"level": "debug"},
		"server": map[string]any{
			"listen_addr": "0.0.0.0:8443",
			"tls": map[string]any{
				"cert_file": controllerDir + "/controller.pem",
				"key_file":  controllerDir + "/controller-key.pem",
				"ca_file":   controllerDir + "/ca.pem",
			},
			"admin": map[string]any{"users": []string{ControllerAdmin}, "groups": o.AdminGroups},
			"login": map[string]any{
				"type": "basic",
				"basic_auth_options": []map[string]any{{
					"username": ControllerAdmin, "password": ControllerPassword, "groups": []string{"admins"},
				}},
			},
			"jwt_maker": map[string]any{
				"alg": "RS256", "private_key": jwtPrivatePem, "issuer": "controller-e2e", "audience": "flock",
			},
		},
		"mtls": map[string]any{
			"cert_file":   controllerDir + "/agent-client.pem",
			"key_file":    controllerDir + "/agent-client-key.pem",
			"ca_file":     controllerDir + "/ca.pem",
			"server_name": AgentServerName,
		},
		"store": map[string]any{"sql": map[string]any{
			"type": "sqlite", "dsn": "/tmp/flock.db", "secret_key": "e2e-secret",
		}},
		"instantiators": []map[string]any{{
			"name":           "static",
			"zones":          []string{o.Zone},
			"resource_types": []map[string]any{{"name": o.ResourceType}},
			"static":         map[string]any{"agents": agents},
		}},
	}
	return yaml.Marshal(conf)
}

// AgentUuid is the uuid a controller of the env, real or fake, declares the agent named name under. It is derived from
// the name so an agent started again keeps it.
func AgentUuid(name string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}

func jwtPrivateKey() (string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	privateDer, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDer})), nil
}

// Target is where agents on the network dial the controller
func (c *Controller) Target() string {
	return c.ip + ":8443"
}

// HostAddr is the address of the controller from the tests, read on each call as a restart changes it
func (c *Controller) HostAddr(ctx context.Context) string {
	addr, err := c.Container.PortEndpoint(ctx, controllerPort, "")
	if err != nil {
		return fmt.Sprintf("unavailable: %v", err)
	}
	return addr
}

// AdminToken signs a break-glass token the way an operator with a shell on the controller host does. Only the token
// is on stdout, the warning on stderr is shown when the command fails.
func (c *Controller) AdminToken(ctx context.Context, reason string) (string, error) {
	script := `flock controller admin-token -c "$0" --reason "$1" 2>/tmp/admin-token.err || { cat /tmp/admin-token.err; exit 1; }`
	out, err := execIn(ctx, c.Container, "sh", "-c", script, controllerConfigPath, reason)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (c *Controller) Logs(ctx context.Context) string {
	return containerLogs(ctx, c.Container)
}

type Api struct {
	clients     *helpers.GrpcClients
	Zone        svczoneconnect.ZoneClient
	Instance    svcinstanceconnect.InstanceClient
	VIP         svcvipconnect.VIPClient
	VS          svcvsconnect.VirtualServerClient
	Backend     svcbackendconnect.BackendClient
	Lookup      svclookupconnect.LookupClient
	Certificate svccertconnect.CertificateClient
	Secret      svcsecretconnect.SecretClient
}

func (a *Api) Close() {
	a.clients.Close()
}

func (c *Controller) LoginAdmin(ctx context.Context) (*Api, error) {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(c.certs.CAPem)
	clients := helpers.NewGrpcClients(&tls.Config{RootCAs: pool, ServerName: "localhost"})
	tok, err := svcloginconnect.NewLoginClient(clients.Client(c.HostAddr(ctx))).GetToken(ctx, &svclogin.GetTokenRequest{
		Username: ControllerAdmin,
		Password: ControllerPassword,
	})
	if err != nil {
		clients.Close()
		return nil, fmt.Errorf("login admin: %w", err)
	}
	bearer := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: tok.GetToken()})
	client := clients.Client(c.HostAddr(ctx), svclogin.TokenInterceptor(bearer))
	return &Api{
		clients:     clients,
		Zone:        svczoneconnect.NewZoneClient(client),
		Instance:    svcinstanceconnect.NewInstanceClient(client),
		VIP:         svcvipconnect.NewVIPClient(client),
		VS:          svcvsconnect.NewVirtualServerClient(client),
		Backend:     svcbackendconnect.NewBackendClient(client),
		Lookup:      svclookupconnect.NewLookupClient(client),
		Certificate: svccertconnect.NewCertificateClient(client),
		Secret:      svcsecretconnect.NewSecretClient(client),
	}, nil
}
