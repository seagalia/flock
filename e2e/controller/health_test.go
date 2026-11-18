//go:build e2e

package e2e_test

import (
	"context"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/e2e/controller/env"
	"github.com/seagalia/flock/sdk/rpcserver"
)

// unhealthyTimeout covers a dependency check of the controller timing out (3s) a few times
const unhealthyTimeout = 30 * time.Second

var _ = Describe("Health", func() {
	It("is serving on sqlite", func(ctx SpecContext) {
		g := startController(ctx, env.ControllerOptions{Name: "controller-sqlite", Store: env.StoreSqlite})
		Expect(healthGet(ctx, g)).To(Equal(http.StatusOK))
	}, NodeTimeout(10*time.Minute))

	Describe("with postgres and nats", func() {
		var (
			g            *env.Controller
			dsn, natsURL string
		)

		BeforeEach(func(ctx SpecContext) {
			var err error
			dsn, err = e2eEnv.StartPostgres(ctx)
			Expect(err).NotTo(HaveOccurred())
			natsURL, err = e2eEnv.StartNats(ctx)
			Expect(err).NotTo(HaveOccurred())
			g = startController(ctx, env.ControllerOptions{Name: "controller-health", Store: env.StorePostgres, DSN: dsn, NatsURL: natsURL})
			Expect(healthGet(ctx, g)).To(Equal(http.StatusOK))
		}, NodeTimeout(10*time.Minute))

		It("is not serving once nats is gone", func(ctx SpecContext) {
			Expect(e2eEnv.StopDependency(ctx, natsURL)).To(Succeed())
			Eventually(healthGet, unhealthyTimeout).WithContext(ctx).WithArguments(g).Should(Equal(http.StatusServiceUnavailable))
		}, NodeTimeout(time.Minute))

		It("is not serving once the database is gone", func(ctx SpecContext) {
			Expect(e2eEnv.StopDependency(ctx, dsn)).To(Succeed())
			Eventually(healthGet, unhealthyTimeout).WithContext(ctx).WithArguments(g).Should(Equal(http.StatusServiceUnavailable))
		}, NodeTimeout(time.Minute))
	})
})

// healthGet gives the http code of the health endpoint, asked over HTTP/1.1 with no credentials as a load balancer
// probe would
func healthGet(ctx context.Context, g *env.Controller) int {
	GinkgoHelper()
	// a transport with its own tls config does not attempt HTTP/2
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: e2eEnv.Certs.UserTLSConfig(env.HostServerName)},
		Timeout:   30 * time.Second,
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+g.Addr(ctx)+rpcserver.HealthPath, nil)
	Expect(err).NotTo(HaveOccurred())
	resp, err := client.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	Expect(resp.ProtoMajor).To(Equal(1))
	return resp.StatusCode
}
