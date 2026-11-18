//go:build e2e

package dev_test

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/seagalia/flock/cli/app"
	"github.com/seagalia/flock/sdk/clients"
	"google.golang.org/protobuf/types/known/emptypb"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("flock dev in an unprivileged container", func() {
	It("serves the quickstart service through envoy", func(ctx SpecContext) {
		Expect(flock(ctx, "apply", "-f", "hello.yml", "--yes")).Error().ToNot(HaveOccurred())

		Eventually(func() (int, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, vsURL+"/", nil)
			if err != nil {
				return 0, err
			}
			req.Host = "hello.example.com"
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return 0, err
			}
			resp.Body.Close()
			return resp.StatusCode, nil
		}).Should(Equal(http.StatusOK))

		Expect(flock(ctx, "apply", "-f", "hello.yml", "--yes")).To(ContainSubstring("Nothing to change"))
	})

	It("runs a provisioner the controller reaches", func(ctx SpecContext) {
		// the uuid flock dev declares its provisioner under, in devenv/configs.go
		const provisionerUuid = "10876b6f-87f9-439c-b3a3-62f0368694cb"
		// an unreachable agent gets an ERRORED state named after it from the controller
		Eventually(func() (string, error) {
			return flock(ctx, "status-agents", "local", "-a", provisionerUuid, "--json")
		}).Should(SatisfyAll(ContainSubstring(`"local-provisioner"`), Not(ContainSubstring("ERRORED"))))
	})

	It("answers a login from outside the container with the generated password", func(ctx SpecContext) {
		password, err := execIn(ctx, devContainer, "cat", "/var/lib/flock/admin-password")
		Expect(err).ToNot(HaveOccurred())
		host, err := devContainer.PortEndpoint(ctx, "8443/tcp", "")
		Expect(err).ToNot(HaveOccurred())

		conn, _, err := app.CreateClient(ctx, filepath.Join(GinkgoT().TempDir(), "config.json"), &app.Config{
			Host:       host,
			Username:   "admin",
			Password:   strings.TrimSpace(password),
			SkipVerify: true,
		}, false)
		Expect(err).ToNot(HaveOccurred())

		me, err := clients.New(conn).Login().Me(ctx, &emptypb.Empty{})
		Expect(err).ToNot(HaveOccurred())
		Expect(me.GetUsername()).To(Equal("admin"))
	})

	It("keeps the pki and the password across a restart", func(ctx SpecContext) {
		readState := func() (ca, password string) {
			ca, err := execIn(ctx, devContainer, "cat", "/var/lib/flock/pki/ca.pem")
			Expect(err).ToNot(HaveOccurred())
			password, err = execIn(ctx, devContainer, "cat", "/var/lib/flock/admin-password")
			Expect(err).ToNot(HaveOccurred())
			return ca, password
		}
		caBefore, passwordBefore := readState()
		readyBefore := strings.Count(containerLogs(ctx, devContainer), "flock is ready")

		timeout := 30 * time.Second
		Expect(devContainer.Stop(ctx, &timeout)).To(Succeed())
		Expect(devContainer.Start(ctx)).To(Succeed())
		Eventually(func() int {
			return strings.Count(containerLogs(ctx, devContainer), "flock is ready")
		}, 2*time.Minute).Should(BeNumerically(">", readyBefore))

		caAfter, passwordAfter := readState()
		Expect(caAfter).To(Equal(caBefore))
		Expect(passwordAfter).To(Equal(passwordBefore))

		// the mapped port changes with the restart
		var err error
		vsURL, err = devContainer.PortEndpoint(ctx, "8080/tcp", "http")
		Expect(err).ToNot(HaveOccurred())
		Eventually(func() (int, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, vsURL+"/", nil)
			if err != nil {
				return 0, err
			}
			req.Host = "hello.example.com"
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return 0, err
			}
			resp.Body.Close()
			return resp.StatusCode, nil
		}, time.Minute).Should(Equal(http.StatusOK))
	})
})
