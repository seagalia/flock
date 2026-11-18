//go:build e2e

package dev_test

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/seagalia/flock/e2e/images"
)

const envoyImage = "envoyproxy/envoy:v1.40.0"

// runScript drops the symlink flock dev would otherwise download envoy to, then starts flock dev as the image's CMD
// does. -f keeps the symlink call valid on a restart of the same container.
const runScript = `#!/bin/sh
mkdir -p /root/.flock/bin
ln -sf /usr/local/bin/envoy /root/.flock/bin/envoy
if [ -n "$START_PORTSERVE" ]; then
  /usr/local/bin/portserve -from 9000 -count 1 &
fi
exec flock dev --data-dir /var/lib/flock --listen-host 0.0.0.0 --envoy-path /usr/local/bin/envoy --no-browser "$@"
`

// quickstart is the resource file of the quickstart of the documentation
const quickstart = `kind: backend
name: hello-backend
ip_addr_group:
  ip_addresses:
  - ip: 127.0.0.1
    port: 9000
health_check:
  tcp_health_check: {}
---
kind: vs
name: hello
port: 8080
http_routes_config:
  virtual_hosts:
  - domains: [hello.example.com]
    routes:
    - name: default
      match: {prefix: /}
      backend_uuid: name:hello-backend
---
kind: vip
name: local
ip: 0.0.0.0
typed_announce:
  '@type': type.googleapis.com/flock.extensions.announces.starling_announce.v1.StarlingAnnounce
  type_announce: STATIC
virtual_servers_uuid:
- name:hello
`

var (
	devContainer testcontainers.Container
	// devImage is envoy with the tools of the privileged mode (ipvsadm, ip, nft), clientImage the e2e client
	devImage, clientImage  string
	flockBin, portserveBin string
	// vsURL reaches the port 8080 of the quickstart virtual server, envoy listens on every interface in dev mode
	vsURL string
)

func TestDev(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(30 * time.Second)
	SetDefaultEventuallyPollingInterval(500 * time.Millisecond)
	RunSpecs(t, "flock dev e2e Suite")
}

// The container is not privileged and keeps the default capabilities, as a user trying flock dev in docker would
// run it: no NET_ADMIN, no nflog, no IPVS.
var _ = BeforeSuite(func(ctx SpecContext) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	Expect(err).ToNot(HaveOccurred())
	repoRoot := strings.TrimSpace(string(out))
	flockBin = filepath.Join(repoRoot, "e2e", ".build", "flock-dev")
	Expect(images.BuildLinuxBinary(ctx, repoRoot, ".", flockBin)).To(Succeed())
	portserveBin = filepath.Join(repoRoot, "e2e", ".build", "portserve")
	Expect(images.BuildLinuxBinary(ctx, repoRoot, "./e2e/load/portserve", portserveBin)).To(Succeed())
	devImage, err = images.Build(ctx, repoRoot, "e2e/dev/docker/dev.Dockerfile", "dev")
	Expect(err).ToNot(HaveOccurred())
	clientImage, err = images.Build(ctx, repoRoot, "e2e/agent/starling/docker/client.Dockerfile", "client")
	Expect(err).ToNot(HaveOccurred())

	devContainer, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: devRequest(true, "/root/hello.yml", quickstart),
		Started:          true,
	})
	DeferCleanup(func(ctx SpecContext) {
		if devContainer != nil && CurrentSpecReport().Failed() {
			GinkgoWriter.Println(containerLogs(ctx, devContainer))
		}
		_ = testcontainers.TerminateContainer(devContainer)
	})
	Expect(err).ToNot(HaveOccurred())

	vsURL, err = devContainer.PortEndpoint(ctx, "8080/tcp", "http")
	Expect(err).ToNot(HaveOccurred())
}, NodeTimeout(10*time.Minute))

// devRequest runs flock dev the way the image's CMD does. The envoy of the image stands for the one flock dev
// downloads, so the suite doesn't depend on github. portserve, the web server of the quickstart, runs in the
// container only when withPortserve is set.
func devRequest(withPortserve bool, filePath, fileContent string, args ...string) testcontainers.ContainerRequest {
	env := map[string]string{}
	if withPortserve {
		env["START_PORTSERVE"] = "1"
	}
	return testcontainers.ContainerRequest{
		Image:        devImage,
		ExposedPorts: []string{"8080/tcp", "8443/tcp"},
		Env:          env,
		Entrypoint:   []string{"/bin/sh", "/usr/local/bin/run-dev.sh"},
		Cmd:          args,
		Files: []testcontainers.ContainerFile{
			{HostFilePath: flockBin, ContainerFilePath: "/usr/local/bin/flock", FileMode: 0o755},
			{HostFilePath: portserveBin, ContainerFilePath: "/usr/local/bin/portserve", FileMode: 0o755},
			fileFromString(runScript, "/usr/local/bin/run-dev.sh", 0o755),
			fileFromString(fileContent, filePath, 0o644),
		},
		WaitingFor: wait.ForLog("flock is ready").WithStartupTimeout(2 * time.Minute),
	}
}

func fileFromString(content, path string, mode int64) testcontainers.ContainerFile {
	return testcontainers.ContainerFile{Reader: strings.NewReader(content), ContainerFilePath: path, FileMode: mode}
}

// flock runs the cli in the container, where it uses the cli config of the dev session
func flock(ctx context.Context, args ...string) (string, error) {
	return flockIn(ctx, devContainer, args...)
}

func flockIn(ctx context.Context, c testcontainers.Container, args ...string) (string, error) {
	cmd := append([]string{"flock"}, args...)
	code, reader, err := c.Exec(ctx, cmd, tcexec.Multiplexed(), tcexec.WithWorkingDir("/root"))
	if err != nil {
		return "", err
	}
	out, _ := io.ReadAll(reader)
	if code != 0 {
		return string(out), fmt.Errorf("flock %v: exit code %d: %s", args, code, out)
	}
	return string(out), nil
}

func execIn(ctx context.Context, c testcontainers.Container, cmd ...string) (string, error) {
	code, reader, err := c.Exec(ctx, cmd, tcexec.Multiplexed())
	if err != nil {
		return "", err
	}
	out, _ := io.ReadAll(reader)
	if code != 0 {
		return string(out), fmt.Errorf("%v: exit code %d: %s", cmd, code, out)
	}
	return string(out), nil
}

func containerLogs(ctx context.Context, c testcontainers.Container) string {
	reader, err := c.Logs(ctx)
	if err != nil {
		return fmt.Sprintf("failed to get logs: %s", err)
	}
	defer reader.Close()
	logs, _ := io.ReadAll(reader)
	return string(logs)
}
