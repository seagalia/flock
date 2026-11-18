//go:build e2e

package env

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.yaml.in/yaml/v2"

	"github.com/seagalia/flock/e2e/images"
	provisionerconfig "github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
)

const (
	pebbleImage         = "ghcr.io/letsencrypt/pebble:2.10.1"
	challtestsrvImage   = "ghcr.io/letsencrypt/pebble-challtestsrv:2.10.1"
	pebbleMinicaPath    = "/test/certs/pebble.minica.pem"
	challtestsrvDnsPort = "8053"
	// the default port of the management API of challtestsrv, where txtshim sets the TXT records
	challtestsrvManagementPort = "8055"
	challtestsrvCallTimeout    = 10 * time.Second

	provisionerConfigPath = "/etc/flock/provisioner.yml"
	provisionerTlsDir     = "/etc/flock/tls"
	provisionerPebbleCA   = "/etc/flock/pebble-ca.pem"
	// the challenge ports pebble validates on, from its default config
	provisionerHttp01Port    = 5002
	provisionerTlsAlpn01Port = 5001
	provisionerTxtShimPath   = "/usr/local/bin/txtshim"
	txtShimBuildPath         = "e2e/.build/txtshim"
	// TxtShimEndpoint is where a provisioner started with Dns01 takes the TXT records of lego's httpreq provider, the
	// value of the certificate metadata flock.acme.httpreq.endpoint
	TxtShimEndpoint = "http://" + txtShimListen
	txtShimListen   = "127.0.0.1:8090"
)

// ChallTestSrv is the DNS server pebble resolves the domains with
type ChallTestSrv struct {
	Container testcontainers.Container
}

// StartChallTestSrv runs the DNS server pebble resolves the domains with: every name points at answerIP, where the
// provisioner answers the challenges, unless ResolveA points it elsewhere. Its other servers are off, they would take
// the challenge ports.
func (e *Env) StartChallTestSrv(ctx context.Context, ip, answerIP string) (*ChallTestSrv, error) {
	c, err := e.start(ctx, ip, testcontainers.ContainerRequest{
		Image: challtestsrvImage,
		Cmd: []string{
			"-defaultIPv4", answerIP, "-defaultIPv6", "",
			"-dnsserver", ":" + challtestsrvDnsPort,
			"-http01", "", "-https01", "", "-tlsalpn01", "", "-doh", "",
		},
		// the tests reach the management API through the mapped port, the network of the containers is not routed
		// to the host
		ExposedPorts:   []string{challtestsrvManagementPort + "/tcp"},
		ConfigModifier: withHostname("challtestsrv"),
		WaitingFor:     wait.ForLog("Starting challenge servers"),
	})
	if err != nil {
		return nil, err
	}
	return &ChallTestSrv{Container: c}, nil
}

// ResolveA makes domain resolve to ip alone instead of the default answer
func (s *ChallTestSrv) ResolveA(ctx context.Context, domain, ip string) error {
	ctx, cancel := context.WithTimeout(ctx, challtestsrvCallTimeout)
	defer cancel()
	endpoint, err := s.Container.PortEndpoint(ctx, challtestsrvManagementPort+"/tcp", "http")
	if err != nil {
		return fmt.Errorf("challtestsrv management endpoint: %w", err)
	}
	payload, err := json.Marshal(map[string]any{"host": domain, "addresses": []string{ip}})
	if err != nil {
		return fmt.Errorf("encode a record of %s: %w", domain, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/add-a", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build a record request of %s: %w", domain, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("add a record of %s: %w", domain, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("add a record of %s: status %d", domain, resp.StatusCode)
	}
	return nil
}

// Pebble is the test ACME CA of Let's Encrypt, its directory is DirUrl from the containers of the network
type Pebble struct {
	Container testcontainers.Container
	ip        string
}

// StartPebble runs pebble with its default config, resolving the domains to validate with the DNS server at dnsIP
func (e *Env) StartPebble(ctx context.Context, ip, dnsIP string) (*Pebble, error) {
	c, err := e.start(ctx, ip, testcontainers.ContainerRequest{
		Image: pebbleImage,
		Cmd:   []string{"-dnsserver", dnsIP + ":" + challtestsrvDnsPort},
		Env: map[string]string{
			// pebble waits up to 15s before each validation and refuses 5% of the nonces by default, a spec
			// would only wait longer
			"PEBBLE_VA_NOSLEEP":      "1",
			"PEBBLE_WFE_NONCEREJECT": "0",
		},
		ConfigModifier: withHostname("pebble"),
		WaitingFor:     wait.ForLog("Listening on: 0.0.0.0:14000"),
	})
	if err != nil {
		return nil, err
	}
	return &Pebble{Container: c, ip: ip}, nil
}

func (p *Pebble) DirUrl() string {
	return "https://" + p.ip + ":14000/dir"
}

// MinicaPem is the CA of the certificate pebble serves its API with, valid for localhost only
func (p *Pebble) MinicaPem(ctx context.Context) ([]byte, error) {
	reader, err := p.Container.CopyFileFromContainer(ctx, pebbleMinicaPath)
	if err != nil {
		return nil, fmt.Errorf("copy pebble minica: %w", err)
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func (p *Pebble) Logs(ctx context.Context) string {
	return containerLogs(ctx, p.Container)
}

type ProvisionerOptions struct {
	// Name is the CN of its certificate, the controller at ControllerAddr declares it under that name
	Name string
	// IP must be in the range of the agent certificates
	IP             string
	Instance       string
	ControllerAddr string
	Pebble         *Pebble
	// TlsAlpn01 answers the challenges with tls-alpn-01 instead of http-01
	TlsAlpn01 bool
	// Dns01 answers the challenges with dns-01 only, through a txtshim at TxtShimEndpoint setting the records in the
	// challtestsrv at ChallTestSrvIP
	Dns01          bool
	ChallTestSrvIP string
	// LeaseTTL is acme.lease_ttl, the provisioner's default when zero
	LeaseTTL time.Duration
}

type Provisioner struct {
	Container testcontainers.Container
}

// StartProvisioner runs `flock provisioner start` in the image of the controller, acme on pebble
func (e *Env) StartProvisioner(ctx context.Context, o ProvisionerOptions) (*Provisioner, error) {
	image, err := e.imageFrom(ctx, "e2e/controller/docker/controller.Dockerfile", "controller")
	if err != nil {
		return nil, err
	}
	confYaml, err := renderProvisionerConfig(o)
	if err != nil {
		return nil, err
	}
	pebbleCA, err := o.Pebble.MinicaPem(ctx)
	if err != nil {
		return nil, err
	}
	certPem, keyPem, err := e.Certs.AgentCert(o.Name)
	if err != nil {
		return nil, fmt.Errorf("certificate of provisioner %s: %w", o.Name, err)
	}
	req := testcontainers.ContainerRequest{
		Image:      image,
		Entrypoint: []string{"/usr/local/bin/flock", "provisioner", "start", "-c", provisionerConfigPath},
		Env: map[string]string{
			// lego reads them to trust the API of pebble
			"LEGO_CA_CERTIFICATES": provisionerPebbleCA,
			"LEGO_CA_SERVER_NAME":  "localhost",
		},
		ConfigModifier: withHostname("provisioner"),
		Files: []testcontainers.ContainerFile{
			fileFromBytes(confYaml, provisionerConfigPath, 0o644),
			fileFromBytes(pebbleCA, provisionerPebbleCA, 0o644),
			fileFromBytes(certPem, provisionerTlsDir+"/cert.pem", 0o644),
			fileFromBytes(keyPem, provisionerTlsDir+"/key.pem", 0o600),
			fileFromBytes(e.Certs.CAPem, provisionerTlsDir+"/ca.pem", 0o644),
		},
		WaitingFor: wait.ForLog("Starting acme cert manager"),
	}
	if o.Dns01 {
		err = e.withTxtShim(ctx, &req, o.ChallTestSrvIP)
		if err != nil {
			return nil, err
		}
	}
	c, err := e.start(ctx, o.IP, req)
	if err != nil {
		return nil, err
	}
	return &Provisioner{Container: c}, nil
}

// withTxtShim runs txtshim beside the provisioner in its container, both logging to the container logs
func (e *Env) withTxtShim(ctx context.Context, req *testcontainers.ContainerRequest, challTestSrvIP string) error {
	err := images.BuildLinuxBinary(ctx, e.repoRoot, "./e2e/provisioner/txtshim", txtShimBuildPath)
	if err != nil {
		return fmt.Errorf("build txtshim: %w", err)
	}
	req.Files = append(req.Files, testcontainers.ContainerFile{
		HostFilePath:      filepath.Join(e.repoRoot, txtShimBuildPath),
		ContainerFilePath: provisionerTxtShimPath,
		FileMode:          0o755,
	})
	management := "http://" + challTestSrvIP + ":" + challtestsrvManagementPort
	script := fmt.Sprintf("%s -listen %s -management %s & exec %s", provisionerTxtShimPath, txtShimListen, management,
		strings.Join(req.Entrypoint, " "))
	req.Entrypoint = []string{"sh", "-c", script}
	req.WaitingFor = wait.ForAll(req.WaitingFor, wait.ForLog("txtshim listening on"))
	return nil
}

func renderProvisionerConfig(o ProvisionerOptions) ([]byte, error) {
	conf := provisionerconfig.Config{
		Log: agentconfig.Log{Level: "debug"},
		ControllerConfig: &agentconfig.ControllerConfig{
			Addr:         o.ControllerAddr,
			ServerName:   ControllerServerName,
			InstanceUuid: o.Instance,
		},
		TlsConfig: &agentconfig.TlsConfig{
			CertFile: provisionerTlsDir + "/cert.pem",
			KeyFile:  provisionerTlsDir + "/key.pem",
			CaFile:   provisionerTlsDir + "/ca.pem",
		},
		Acme: provisionerconfig.AcmeConfig{
			Enabled:         true,
			DefaultCADirUrl: o.Pebble.DirUrl(),
			LeaseTTL:        o.LeaseTTL,
			Challenges: provisionerconfig.Challenges{
				Http01Enabled:    !o.TlsAlpn01 && !o.Dns01,
				TlsAlpn01Enabled: o.TlsAlpn01,
				Http01Port:       provisionerHttp01Port,
				TlsAlpn01Port:    provisionerTlsAlpn01Port,
				// challtestsrv serves a TXT record as soon as it is set, and the provisioner can't query the
				// authoritative servers of the e2e domains
				Dns01SkipPropagationCheck: o.Dns01,
			},
		},
	}
	return yaml.Marshal(conf)
}

func (p *Provisioner) Logs(ctx context.Context) string {
	return containerLogs(ctx, p.Container)
}
