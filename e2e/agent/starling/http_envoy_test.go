//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("HTTP through Envoy", func() {
	const (
		vipIP = "10.250.0.30"
		url   = "http://" + vipIP
	)

	BeforeEach(func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-app", []string{httpbin1IP + ":8080"}),
			res.Backend("c-other", []string{httpbin2IP + ":8080"}),
			res.HttpVs("vs-http", 80, res.HttpRoute(res.VirtualHost([]string{"app.e2e.local"},
				res.WithRedirect("old", "/old", "https://app.e2e.local/new")), "c-app"), res.HttpRoute(res.VirtualHost([]string{"other.e2e.local"},
				res.WithDirectResponse("maint", "/maint", 503, "maintenance")), "c-other"),
				res.VirtualHost([]string{"prefixed.e2e.local"},
					res.WithPathPrefix("/api"), res.WithRedirect("api-root", "/", "https://prefixed.e2e.local/moved"))),
			res.Vip("vip-http", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-http"),
		)
	})

	It("routes by host", func(ctx SpecContext) {
		Eventually(func() (string, error) {
			return client.Hostname(ctx, url+"/hostname", env.WithHeader("Host: app.e2e.local"))
		}).Should(Equal("httpbin-1"))
		Eventually(func() (string, error) {
			return client.Hostname(ctx, url+"/hostname", env.WithHeader("Host: other.e2e.local"))
		}).Should(Equal("httpbin-2"))
	})

	It("redirects", func(ctx SpecContext) {
		Eventually(func(g Gomega) {
			status, location, err := client.Redirect(ctx, url+"/old", env.WithHeader("Host: app.e2e.local"))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(status).To(BeNumerically(">=", 300))
			g.Expect(status).To(BeNumerically("<", 400))
			g.Expect(location).To(Equal("https://app.e2e.local/new"))
		}).Should(Succeed())
	})

	It("matches the path prefix of a virtual host on whole path segments", func(ctx SpecContext) {
		statusOf := func(path string) func() (int, error) {
			return func() (int, error) {
				status, _, err := client.Redirect(ctx, url+path, env.WithHeader("Host: prefixed.e2e.local"))
				return status, err
			}
		}
		Eventually(statusOf("/api")).Should(Equal(301))
		Eventually(statusOf("/api/users")).Should(Equal(301))
		Eventually(statusOf("/apiv2")).Should(Equal(404))
	})

	It("answers direct responses", func(ctx SpecContext) {
		Eventually(func(g Gomega) {
			status, body, err := client.HTTPGet(ctx, url+"/maint", env.WithHeader("Host: other.e2e.local"))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(status).To(Equal(503))
			g.Expect(body).To(Equal("maintenance"))
		}).Should(Succeed())
	})

	It("is served by envoy and not by ipvs", func(ctx SpecContext) {
		Eventually(func() []string { return main.EnvoyListeners(ctx) }).Should(ContainElement("tcp-80/vip-http"))
		Consistently(func() []env.IpvsService { return main.Ipvs(ctx) }, "2s").Should(BeEmpty())
	})
})
