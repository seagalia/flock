//go:build e2e

package env

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.yaml.in/yaml/v2"
)

const (
	controllerConfigPath = "/etc/flock/controller.yml"
	controllerTlsDir     = "/etc/flock/tls"
	controllerPort       = "8443/tcp"
	controllerReadyLog   = "Starting grpc and http server"

	AdminUser     = "admin"
	AdminPassword = "admin"
	AdminGroup    = "admins"
	JwtIssuer     = "controller-e2e"
	JwtAudience   = "flock"
)

type Store string

const (
	StoreSqlite   Store = "sqlite"
	StorePostgres Store = "postgres"
	StoreMysql    Store = "mysql"
)

// what the oidc login of RenderControllerConfig expects from the tokens of ControllerOptions.Oidc
const (
	OidcClientId = "flock"
	OidcAudience = "flock-api"
	oauth2Port   = "8080/tcp"
)

// Oauth2 is the OIDC provider a controller can log users in with
type Oauth2 struct {
	// Name is the container name on the network, the issuer is http://<Name>:8080/default
	Name      string
	Container testcontainers.Container
}

// IssuerURL is also the token iss, the provider builds it from the host it is called on
func (o *Oauth2) IssuerURL() string {
	return fmt.Sprintf("http://%s:8080/default", o.Name)
}

// Addr is the host side address of the provider, where IssuerURL points at from the containers
func (o *Oauth2) Addr(ctx context.Context) string {
	addr, err := o.Container.PortEndpoint(ctx, oauth2Port, "")
	if err != nil {
		return fmt.Sprintf("unavailable-%s: %v", o.Name, err)
	}
	return addr
}

type ControllerOptions struct {
	// Name is the container name on the network. The tests dial controller from the host and verify HostServerName,
	// so the name does not need to be in the certificate
	Name    string
	Store   Store
	DSN     string // of the postgres or mysql store, a sqlite store needs none
	NatsURL string
	// a controller has one login service: oidc with Oidc, ldap with Ldap, basic with the admin user when both are nil
	Oidc          *Oauth2
	Ldap          *Ldap
	Instantiators []map[string]any // rendered as-is under `instantiators`
	Ipam          map[string]any   // rendered as-is under `ipam`, nil keeps the internal ipam
	Dashboard     bool
	HostPorts     []int  // host ports controller reaches as host.testcontainers.internal
	LogLevel      string // empty means debug
}

// Controller is a real flock-controller running in a container
type Controller struct {
	Container testcontainers.Container
	opts      ControllerOptions
	certs     *Certs
}

// RenderControllerConfig gives the controller yaml config, dir is where the files of certs.WriteFiles are.
// It is built from maps because config.Config does not marshal back to what it reads.
func RenderControllerConfig(o ControllerOptions, certs *Certs, dir string) ([]byte, error) {
	needsDsn := o.Store != StoreSqlite
	if needsDsn && o.DSN == "" {
		return nil, fmt.Errorf("controller %s: %s store needs a dsn", o.Name, o.Store)
	}
	if o.Oidc != nil && o.Ldap != nil {
		return nil, fmt.Errorf("controller %s: oidc and ldap given, a controller has one login service", o.Name)
	}
	inDir := func(name string) string { return filepath.Join(dir, name) }

	login := map[string]any{
		"type": "basic",
		"basic_auth_options": []map[string]any{{
			"username": AdminUser, "password": AdminPassword, "groups": []string{AdminGroup},
		}},
	}
	if o.Ldap != nil {
		login = o.Ldap.login()
	}
	if o.Oidc != nil {
		login = map[string]any{
			"type": "oidc",
			"oidc_options": map[string]any{
				"issuer_url":   o.Oidc.IssuerURL(),
				"client_id":    OidcClientId,
				"access_token": map[string]any{"audience": OidcAudience},
				// the mock provider only speaks http, on its container name
				"insecure_http": true,
			},
		}
	}

	// a sqlite store lives and dies with its container
	dsn := "/tmp/flock.db"
	if needsDsn {
		dsn = o.DSN
	}
	store := map[string]any{"sql": map[string]any{
		"type": string(o.Store), "dsn": dsn, "secret_key": "e2e-secret",
	}}
	logLevel := o.LogLevel
	if logLevel == "" {
		logLevel = "debug"
	}
	conf := map[string]any{
		"log": map[string]any{"level": logLevel},
		"server": map[string]any{
			"listen_addr": "0.0.0.0:8443",
			"tls": map[string]any{
				"cert_file": inDir("controller.pem"), "key_file": inDir("controller-key.pem"), "ca_file": inDir("ca.pem"),
			},
			"admin": map[string]any{"users": []string{AdminUser}, "groups": []string{AdminGroup}},
			"login": login,
			"jwt_maker": map[string]any{
				"alg": "RS256", "private_key": string(certs.JwtPrivateKeyPem), "issuer": JwtIssuer, "audience": JwtAudience,
			},
			"dashboard": map[string]any{"enabled": o.Dashboard},
		},
		"mtls": map[string]any{
			"cert_file": inDir("controller-agent.pem"), "key_file": inDir("controller-agent-key.pem"), "ca_file": inDir("ca.pem"),
			"server_name": agentServerName,
		},
		"store": store,
	}
	if o.NatsURL != "" {
		conf["multi"] = map[string]any{"nats": map[string]any{"url": o.NatsURL}}
	}
	if len(o.Instantiators) > 0 {
		conf["instantiators"] = o.Instantiators
	}
	if o.Ipam != nil {
		conf["ipam"] = o.Ipam
	}
	return yaml.Marshal(conf)
}

// SubscriberInstantiators declares the agent of SubscriberTLSConfig for the instances of SubscriberZone, a controller
// with no instantiator refuses every agent. The controller never calls the host since no feature is on.
func SubscriberInstantiators() []map[string]any {
	return []map[string]any{{
		"name":           "e2e",
		"zones":          []string{SubscriberZone},
		"resource_types": []map[string]any{{"name": "small"}},
		"static": map[string]any{"agents": []map[string]any{{
			"uuid": SubscriberUuid, "name": SubscriberName, "host": "host.testcontainers.internal:1",
		}}},
	}}
}

func (e *Env) StartController(ctx context.Context, o ControllerOptions) (*Controller, error) {
	image, err := e.image(ctx, "controller")
	if err != nil {
		return nil, err
	}
	confYaml, err := RenderControllerConfig(o, e.Certs, controllerTlsDir)
	if err != nil {
		return nil, err
	}
	files, err := e.controllerTlsFiles()
	if err != nil {
		return nil, err
	}
	files = append(files, fileFromBytes(confYaml, controllerConfigPath, 0o644))

	c, err := e.start(ctx, o.Name, testcontainers.ContainerRequest{
		Image:           image,
		ExposedPorts:    []string{controllerPort},
		HostAccessPorts: o.HostPorts,
		Files:           files,
		// the log line is printed just before listening, so the port is waited on too, it also covers restarts
		// where the line of the previous run is already in the logs
		WaitingFor: wait.ForAll(wait.ForLog(controllerReadyLog), wait.ForListeningPort(controllerPort)),
	})
	if err != nil {
		return nil, err
	}
	return &Controller{Container: c, opts: o, certs: e.Certs}, nil
}

// controllerTlsFiles reuses the file names of Certs.WriteFiles, the ones RenderControllerConfig points at
func (e *Env) controllerTlsFiles() ([]testcontainers.ContainerFile, error) {
	dir, err := os.MkdirTemp("", "flock-e2e-controller-tls-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	err = e.Certs.WriteFiles(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := make([]testcontainers.ContainerFile, 0, len(entries))
	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, fileFromBytes(content, controllerTlsDir+"/"+entry.Name(), 0o600))
	}
	return files, nil
}

func (g *Controller) Name() string {
	return g.opts.Name
}

// Addr is the host side address of controller, it is read again on every call because a restart changes the mapped port
func (g *Controller) Addr(ctx context.Context) string {
	addr, err := g.Container.PortEndpoint(ctx, controllerPort, "")
	if err != nil {
		// an unusable address makes the dial fail with the reason instead of hiding it
		return fmt.Sprintf("unavailable-%s: %v", g.opts.Name, err)
	}
	return addr
}

func (g *Controller) Restart(ctx context.Context) error {
	err := stopContainer(ctx, g.Container)
	if err != nil {
		return fmt.Errorf("stop controller %s: %w", g.opts.Name, err)
	}
	err = g.Container.Start(ctx)
	if err != nil {
		return fmt.Errorf("start controller %s: %w", g.opts.Name, err)
	}
	return nil
}

// Terminate removes the container, so a later spec can reuse the name on the network
func (g *Controller) Terminate(ctx context.Context) error {
	return g.Container.Terminate(ctx)
}

func (g *Controller) Logs(ctx context.Context) string {
	return containerLogs(ctx, g.Container)
}
