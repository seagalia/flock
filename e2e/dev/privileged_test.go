//go:build e2e

package dev_test

import (
	"fmt"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
)

// privilegedFile is the quickstart with the backend on the backend container. The virtual server is a tcp one because
// IPVS only serves tcp and udp, envoy serves the http ones.
const privilegedFile = `kind: backend
name: hello-backend
ip_addr_group:
  ip_addresses:
  - ip: %s
    port: 9000
health_check:
  tcp_health_check: {}
---
kind: vs
name: hello
port: 8080
tcp_route_config:
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

var _ = Describe("flock dev in a privileged container", Ordered, func() {
	var (
		dev, backend testcontainers.Container
		devIP        string
	)

	BeforeAll(func(ctx SpecContext) {
		dockerNet, err := network.New(ctx)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) { _ = dockerNet.Remove(ctx) })

		backend, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:    clientImage,
				Networks: []string{dockerNet.Name},
				Cmd:      []string{"/usr/local/bin/portserve", "-from", "9000", "-count", "1"},
				Files: []testcontainers.ContainerFile{
					{HostFilePath: portserveBin, ContainerFilePath: "/usr/local/bin/portserve", FileMode: 0o755},
				},
			},
			Started: true,
		})
		DeferCleanup(func(ctx SpecContext) { _ = testcontainers.TerminateContainer(backend) })
		Expect(err).ToNot(HaveOccurred())
		backendIP, err := backend.ContainerIP(ctx)
		Expect(err).ToNot(HaveOccurred())

		req := devRequest(false, "/root/hello.yml", "", "--privileged", "--yes")
		req.Privileged = true
		req.Networks = []string{dockerNet.Name}
		dev, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		DeferCleanup(func(ctx SpecContext) {
			if dev != nil && CurrentSpecReport().Failed() {
				GinkgoWriter.Println(containerLogs(ctx, dev))
				for _, cmd := range [][]string{{"ipvsadm", "-Ln"}, {"nft", "list", "table", "inet", "flock"}, {"ip", "addr"}} {
					out, _ := execIn(ctx, dev, cmd...)
					GinkgoWriter.Printf("$ %v\n%s\n", cmd, out)
				}
			}
			_ = testcontainers.TerminateContainer(dev)
		})
		Expect(err).ToNot(HaveOccurred())
		devIP, err = dev.ContainerIP(ctx)
		Expect(err).ToNot(HaveOccurred())

		file := fmt.Sprintf(privilegedFile, backendIP)
		Expect(dev.CopyToContainer(ctx, []byte(file), "/root/hello.yml", 0o644)).To(Succeed())
	}, NodeTimeout(5*time.Minute))

	It("serves the wildcard vip through IPVS on the container's IP", func(ctx SpecContext) {
		Expect(flockIn(ctx, dev, "apply", "-f", "hello.yml", "--yes")).Error().ToNot(HaveOccurred())

		Eventually(func() (string, error) {
			return execIn(ctx, backend, "curl", "-s", "-m", "5", "-o", "/dev/null", "-w", "%{http_code}",
				"-H", "Host: hello.example.com", fmt.Sprintf("http://%s:8080/", devIP))
		}, time.Minute).Should(Equal("200"))

		// through docker's published port, as a mac user reaches it
		published, err := dev.PortEndpoint(ctx, "8080/tcp", "http")
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() (int, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, published+"/", nil)
			if err != nil {
				return 0, err
			}
			resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
			if err != nil {
				return 0, err
			}
			resp.Body.Close()
			return resp.StatusCode, nil
		}, 20*time.Second).Should(Equal(http.StatusOK))

		Eventually(func() (string, error) {
			return execIn(ctx, dev, "ipvsadm", "-Ln")
		}).Should(ContainSubstring(devIP + ":8080"))

		// the wildcard is served on the addresses of eth0, it never goes on flock0
		out, err := execIn(ctx, dev, "ip", "-4", "addr", "show", "flock0")
		Expect(err).ToNot(HaveOccurred())
		Expect(out).ToNot(ContainSubstring("inet "))
	})
})
