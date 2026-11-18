//go:build e2e

package env

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	dnetwork "github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/seagalia/flock/sdk/agentsdk/fakecontroller"

	"github.com/seagalia/flock/e2e/images"
)

const (
	ControllerServerName = "controller.e2e.local"

	httpbinImage = "mccutchen/go-httpbin:2.26.0"
)

type Env struct {
	Net *testcontainers.DockerNetwork
	// V6Prefix is the ipv6 /64 of the network, like fd00:31:250::
	V6Prefix       string
	Controller     *fakecontroller.Server
	ControllerPort int
	Certs          *Certs

	repoRoot   string
	mutex      *sync.Mutex
	containers []testcontainers.Container
}

// Setup runs the containers on 172.31.250.0/24, agents connect to the fake controller unless told otherwise
func Setup(ctx context.Context) (*Env, error) {
	return SetupOn(ctx, "172.31.250")
}

// SetupOn runs the containers on the /24 network of prefix, like "172.31.250": .1 is the gateway and the
// certificates of the agents are valid for .100 to .110
func SetupOn(ctx context.Context, prefix string) (*Env, error) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		return nil, err
	}
	agentIPs := make([]string, 0)
	for i := 100; i <= 110; i++ {
		agentIPs = append(agentIPs, fmt.Sprintf("%s.%d", prefix, i))
	}
	certs, err := NewCerts(ControllerServerName, agentIPs...)
	if err != nil {
		return nil, fmt.Errorf("certs: %w", err)
	}

	// dual stack on fd00:<b>:<c>::/64 for the prefix a.b.c: docker gives addresses only in its first /112, the rest
	// of the /64 is free for vips
	octets := strings.Split(prefix, ".")
	v6Prefix := fmt.Sprintf("fd00:%s:%s::", octets[1], octets[2])
	dockerNet, err := network.New(ctx, network.WithEnableIPv6(), network.WithIPAM(&dnetwork.IPAM{
		Config: []dnetwork.IPAMConfig{
			{
				Subnet:  netip.MustParsePrefix(prefix + ".0/24"),
				Gateway: netip.MustParseAddr(prefix + ".1"),
			},
			{
				Subnet:  netip.MustParsePrefix(v6Prefix + "/64"),
				IPRange: netip.MustParsePrefix(v6Prefix + "/112"),
				Gateway: netip.MustParseAddr(v6Prefix + "1"),
			},
		},
	}))
	if err != nil {
		return nil, fmt.Errorf("network: %w", err)
	}

	controller := fakecontroller.New(certs.ControllerTLSConfig())
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("controller listener: %w", err)
	}
	go func() {
		_ = controller.Serve(lis)
	}()

	return &Env{
		Net:            dockerNet,
		V6Prefix:       v6Prefix,
		Controller:     controller,
		ControllerPort: lis.Addr().(*net.TCPAddr).Port,
		Certs:          certs,
		repoRoot:       repoRoot,
		mutex:          &sync.Mutex{},
	}, nil
}

func (e *Env) Teardown(ctx context.Context) {
	e.mutex.Lock()
	containers := e.containers
	e.mutex.Unlock()
	for _, c := range containers {
		_ = c.Terminate(ctx)
	}
	_ = e.Net.Remove(ctx)
	e.Controller.Stop()
}

// ContainerOpt changes the request of a container before it starts
type ContainerOpt func(req *testcontainers.ContainerRequest)

// WithLabels adds labels to the container, the docker provisioner reads them
func WithLabels(labels map[string]string) ContainerOpt {
	return func(req *testcontainers.ContainerRequest) {
		if req.Labels == nil {
			req.Labels = map[string]string{}
		}
		maps.Copy(req.Labels, labels)
	}
}

// StartHttpbin listens on 8080 and answers name on /hostname
func (e *Env) StartHttpbin(ctx context.Context, name, ip string, opts ...ContainerOpt) (testcontainers.Container, error) {
	req := testcontainers.ContainerRequest{
		Image:          httpbinImage,
		Env:            map[string]string{"USE_REAL_HOSTNAME": "true"},
		ConfigModifier: withHostname(name),
		WaitingFor:     wait.ForLog("go-httpbin listening"),
	}
	for _, opt := range opts {
		opt(&req)
	}
	return e.start(ctx, ip, req)
}

// StartHttpsHttpbin is StartHttpbin serving https with the given certificate
func (e *Env) StartHttpsHttpbin(ctx context.Context, name, ip string, certPem, keyPem []byte) (testcontainers.Container, error) {
	return e.start(ctx, ip, testcontainers.ContainerRequest{
		Image: httpbinImage,
		Env: map[string]string{
			"USE_REAL_HOSTNAME": "true",
			"HTTPS_CERT_FILE":   "/tls/cert.pem",
			"HTTPS_KEY_FILE":    "/tls/key.pem",
		},
		Files: []testcontainers.ContainerFile{
			fileFromBytes(certPem, "/tls/cert.pem", 0o644),
			fileFromBytes(keyPem, "/tls/key.pem", 0o644),
		},
		ConfigModifier: withHostname(name),
		WaitingFor:     wait.ForLog("go-httpbin listening"),
	})
}

func (e *Env) StopContainer(ctx context.Context, c testcontainers.Container) error {
	timeout := 5 * time.Second
	return c.Stop(ctx, &timeout)
}

func (e *Env) StartContainer(ctx context.Context, c testcontainers.Container) error {
	return c.Start(ctx)
}

func (e *Env) start(ctx context.Context, ip string, req testcontainers.ContainerRequest) (testcontainers.Container, error) {
	req.Networks = []string{e.Net.Name}
	req.EndpointSettingsModifier = withFixedIP(e.Net.Name, ip)
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if c != nil {
		e.mutex.Lock()
		e.containers = append(e.containers, c)
		e.mutex.Unlock()
	}
	if err != nil {
		return nil, fmt.Errorf("start container %s at %s: %w", req.Image, ip, err)
	}
	return c, nil
}

// image builds e2e/agent/starling/docker/<name>.Dockerfile once, from the repository root
func (e *Env) image(ctx context.Context, name string) (string, error) {
	return e.imageFrom(ctx, "e2e/agent/starling/docker/"+name+".Dockerfile", name)
}

// imageFrom builds dockerfile, relative to the repository root, once as flock-e2e/<name>
func (e *Env) imageFrom(ctx context.Context, dockerfile, name string) (string, error) {
	return images.Build(ctx, e.repoRoot, dockerfile, name)
}

func execIn(ctx context.Context, c testcontainers.Container, cmd ...string) (string, error) {
	code, reader, err := c.Exec(ctx, cmd, tcexec.Multiplexed())
	if err != nil {
		return "", fmt.Errorf("exec %v: %w", cmd, err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("read output of %v: %w", cmd, err)
	}
	if code != 0 {
		return string(out), fmt.Errorf("exec %v: exit code %d: %s", cmd, code, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func containerLogs(ctx context.Context, c testcontainers.Container) string {
	reader, err := c.Logs(ctx)
	if err != nil {
		return fmt.Sprintf("failed to get logs: %s", err)
	}
	defer reader.Close()
	b, _ := io.ReadAll(reader)
	return string(b)
}

func withHostname(name string) func(*container.Config) {
	return func(c *container.Config) {
		c.Hostname = name
	}
}

func withFixedIP(networkName, ip string) func(map[string]*dnetwork.EndpointSettings) {
	return func(settings map[string]*dnetwork.EndpointSettings) {
		endpoint, ok := settings[networkName]
		if !ok || endpoint == nil {
			endpoint = &dnetwork.EndpointSettings{}
			settings[networkName] = endpoint
		}
		endpoint.IPAMConfig = &dnetwork.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(ip)}
	}
}

func fileFromBytes(content []byte, path string, mode int64) testcontainers.ContainerFile {
	return testcontainers.ContainerFile{Reader: bytes.NewReader(content), ContainerFilePath: path, FileMode: mode}
}

func findRepoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("unable to find repository root")
	}
	// walk up to the module root, the e2e suites live at different depths of the monorepo
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("unable to find repository root from %s", file)
		}
		dir = parent
	}
}
