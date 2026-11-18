//go:build e2e

package waf_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/seagalia/flock/e2e/images"
)

const (
	envoyImage = "envoyproxy/envoy:v1.40.0"
	nginxImage = "nginx:1.31-alpine"

	mysqlError = "You have an error in your SQL syntax; check the manual that corresponds to your MySQL server " +
		"version for the right syntax to use near ''' at line 1"
)

// One envoy listener per WAF. The default CRS one sends no x-waf-uuid, the others name a registered config.
var listeners = map[string]int{
	"":       10000,
	"detect": 10001,
	"custom": 10002,
}

var (
	wafContainer testcontainers.Container
	listenerURLs = map[string]string{}
)

func TestWaf(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(30 * time.Second)
	SetDefaultEventuallyPollingInterval(500 * time.Millisecond)
	RunSpecs(t, "flock WAF e2e Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	Expect(err).ToNot(HaveOccurred())
	repoRoot := strings.TrimSpace(string(out))
	wafBin := filepath.Join(repoRoot, "e2e", ".build", "waf")
	Expect(images.BuildLinuxBinary(ctx, repoRoot, "./agent/starling/waf/starling-waf", wafBin)).To(Succeed())
	wafctlBin := filepath.Join(repoRoot, "e2e", ".build", "wafctl")
	Expect(images.BuildLinuxBinary(ctx, repoRoot, "./e2e/waf/wafctl", wafctlBin)).To(Succeed())

	net, err := network.New(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func(ctx SpecContext) { _ = net.Remove(ctx) })

	backend, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:          nginxImage,
			Networks:       []string{net.Name},
			NetworkAliases: map[string][]string{net.Name: {"backend"}},
			Files: []testcontainers.ContainerFile{
				fileFromString(nginxConf, "/etc/nginx/conf.d/default.conf", 0o644),
			},
			WaitingFor: wait.ForLog("start worker process"),
		},
		Started: true,
	})
	DeferCleanup(func(ctx SpecContext) { _ = testcontainers.TerminateContainer(backend) })
	Expect(err).ToNot(HaveOccurred())

	var exposedPorts []string
	for _, port := range listeners {
		exposedPorts = append(exposedPorts, fmt.Sprintf("%d/tcp", port))
	}
	wafContainer, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        envoyImage,
			Networks:     []string{net.Name},
			ExposedPorts: exposedPorts,
			Entrypoint:   []string{"/bin/sh", "/usr/local/bin/run-waf.sh"},
			Files: []testcontainers.ContainerFile{
				{HostFilePath: wafBin, ContainerFilePath: "/usr/local/bin/waf", FileMode: 0o755},
				{HostFilePath: wafctlBin, ContainerFilePath: "/usr/local/bin/wafctl", FileMode: 0o755},
				fileFromString(runScript, "/usr/local/bin/run-waf.sh", 0o755),
				fileFromString(envoyConf(), "/etc/envoy/waf.yaml", 0o644),
			},
			// the CRS WAFs compile for a few seconds before the socket opens
			WaitingFor: wait.ForAll(
				wait.ForLog("starting WAF server"),
				wait.ForListeningPort("10000/tcp"),
			).WithDeadline(2 * time.Minute),
		},
		Started: true,
	})
	DeferCleanup(func(ctx SpecContext) {
		if wafContainer != nil && CurrentSpecReport().Failed() {
			GinkgoWriter.Println(containerLogs(ctx))
		}
		_ = testcontainers.TerminateContainer(wafContainer)
	})
	Expect(err).ToNot(HaveOccurred())

	for wafID, port := range listeners {
		endpoint, err := wafContainer.PortEndpoint(ctx, fmt.Sprintf("%d/tcp", port), "http")
		Expect(err).ToNot(HaveOccurred())
		listenerURLs[wafID] = endpoint
	}
}, NodeTimeout(10*time.Minute))

const runScript = `#!/bin/sh
/usr/local/bin/waf --socket-path /tmp/waf.sock --log-level info &
exec envoy -c /etc/envoy/waf.yaml --log-level warn
`

var nginxConf = `server {
    listen 80;
    default_type text/html;
    location = /leak {
        return 200 "<html><body>` + mysqlError + `</body></html>";
    }
    location / {
        return 200 "<html><body>hello</body></html>";
    }
}
`

// envoyConf follows the ext_proc filter of agent/starling/waf/starling-waf/envoy.yaml, so the WAF sees what it sees in
// production.
func envoyConf() string {
	var conf strings.Builder
	conf.WriteString("static_resources:\n  listeners:\n")
	for wafID, port := range listeners {
		metadata := ""
		if wafID != "" {
			metadata = fmt.Sprintf(`
                        initial_metadata:
                          - key: x-waf-uuid
                            value: %s`, wafID)
		}
		fmt.Fprintf(&conf, `    - name: listener_%d
      address:
        socket_address: { address: 0.0.0.0, port_value: %d }
      filter_chains:
        - filters:
            - name: envoy.filters.network.http_connection_manager
              typed_config:
                "@type": type.googleapis.com/envoy.extensions.filters.network.http_connection_manager.v3.HttpConnectionManager
                stat_prefix: listener_%d
                route_config:
                  virtual_hosts:
                    - name: backend
                      domains: ["*"]
                      routes:
                        - match: { prefix: "/" }
                          route: { cluster: backend }
                http_filters:
                  - name: envoy.filters.http.ext_proc
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.ext_proc.v3.ExternalProcessor
                      grpc_service:
                        envoy_grpc:
                          cluster_name: ext_proc_cluster%s
                      failure_mode_allow: false
                      processing_mode:
                        request_header_mode: SEND
                        response_header_mode: SEND
                        request_body_mode: BUFFERED
                        response_body_mode: BUFFERED_PARTIAL
                      request_attributes:
                        - source.address
                        - source.port
                        - destination.address
                        - destination.port
                        - request.host
                        - request.path
                        - request.method
                        - request.protocol
                  - name: envoy.filters.http.router
                    typed_config:
                      "@type": type.googleapis.com/envoy.extensions.filters.http.router.v3.Router
`, port, port, port, metadata)
	}
	conf.WriteString(`  clusters:
    - name: ext_proc_cluster
      type: STATIC
      connect_timeout: 1s
      typed_extension_protocol_options:
        envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
          "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
          explicit_http_config:
            http2_protocol_options: {}
      load_assignment:
        cluster_name: ext_proc_cluster
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    pipe: { path: /tmp/waf.sock }
    - name: backend
      type: STRICT_DNS
      connect_timeout: 1s
      load_assignment:
        cluster_name: backend
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    socket_address: { address: backend, port_value: 80 }
`)
	return conf.String()
}

func fileFromString(content, path string, mode int64) testcontainers.ContainerFile {
	return testcontainers.ContainerFile{Reader: strings.NewReader(content), ContainerFilePath: path, FileMode: mode}
}

// wafctl runs in the container, the unix socket of the WAF can't be reached from the host.
func wafctl(ctx context.Context, args ...string) error {
	cmd := append([]string{"/usr/local/bin/wafctl"}, args...)
	code, reader, err := wafContainer.Exec(ctx, cmd, tcexec.Multiplexed())
	if err != nil {
		return err
	}
	out, _ := io.ReadAll(reader)
	if code != 0 {
		return fmt.Errorf("wafctl %v: exit code %d: %s", args, code, out)
	}
	return nil
}

func containerLogs(ctx context.Context) string {
	reader, err := wafContainer.Logs(ctx)
	if err != nil {
		return fmt.Sprintf("failed to get logs: %s", err)
	}
	defer reader.Close()
	logs, _ := io.ReadAll(reader)
	return string(logs)
}

// call answers the status code envoy gives for a request through the listener of wafID.
func call(wafID, method, path string, body string, headers map[string]string) int {
	GinkgoHelper()
	req, err := http.NewRequest(method, listenerURLs[wafID]+path, strings.NewReader(body))
	Expect(err).ToNot(HaveOccurred())
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) starling-waf-e2e")
	req.Header.Set("Accept", "text/html")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	Expect(err).ToNot(HaveOccurred())
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}
