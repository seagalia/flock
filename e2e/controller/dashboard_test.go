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
)

// the e2e controller image embeds a stub dashboard: index.html and the hashed bundle main-E2ESTUB1.js
const (
	stubDashboardIndex  = "<html>flock e2e dashboard</html>\n"
	stubDashboardBundle = "main-E2ESTUB1.js"
)

var _ = Describe("Dashboard", func() {
	Describe("enabled", Ordered, func() {
		var g *env.Controller

		BeforeAll(func(ctx SpecContext) {
			g = startController(ctx, env.ControllerOptions{Name: "controller-dashboard", Store: env.StoreSqlite, Dashboard: true})
		}, NodeTimeout(10*time.Minute))

		BeforeEach(func() {
			registerControllerForDump(g)
		})

		It("serves index.html at its root without cache", func(ctx SpecContext) {
			resp, body := dashboardGet(ctx, g, "/dashboard/")
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(body).To(Equal(stubDashboardIndex))
			Expect(resp.Header.Get("Cache-Control")).To(Equal("no-cache"))
			Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/html"))
		}, NodeTimeout(time.Minute))

		It("serves a hashed bundle as immutable", func(ctx SpecContext) {
			resp, body := dashboardGet(ctx, g, "/dashboard/"+stubDashboardBundle)
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(body).To(ContainSubstring("flock e2e bundle"))
			Expect(resp.Header.Get("Cache-Control")).To(Equal("public, max-age=31536000, immutable"))
			Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/javascript"))
		}, NodeTimeout(time.Minute))
	})

	It("is not served when disabled", func(ctx SpecContext) {
		g := startController(ctx, env.ControllerOptions{Name: "controller-no-dashboard", Store: env.StoreSqlite})

		for _, path := range []string{"/dashboard/", "/dashboard/" + stubDashboardBundle} {
			resp, body := dashboardGet(ctx, g, path)
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound), "%s answered %s", path, body)
		}
	}, NodeTimeout(10*time.Minute))
})

// dashboardGet does not follow redirects, so the redirect of the prefix is seen as it is
func dashboardGet(ctx context.Context, g *env.Controller, path string) (*http.Response, string) {
	GinkgoHelper()
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: e2eEnv.Certs.UserTLSConfig(env.HostServerName)},
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+g.Addr(ctx)+path, nil)
	Expect(err).NotTo(HaveOccurred())
	resp, err := client.Do(req)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	Expect(err).NotTo(HaveOccurred())
	return resp, string(body)
}
