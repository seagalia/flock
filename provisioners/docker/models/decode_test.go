package models_test

import (
	"time"

	"github.com/moby/moby/api/types/container"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/provisioners/docker/models"
	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
)

var _ = Describe("Decode", func() {
	It("reads flat labels into the models", func() {
		base, err := models.Decode(container.Summary{ID: "c1", Labels: map[string]string{
			"com.docker.compose.project":                 "shop",
			"com.docker.compose.service":                 "web",
			"io.seagalia.flock.enable":                   "true",
			"io.seagalia.flock.domains":                  "shop.example.com, www.example.com",
			"io.seagalia.flock.path":                     "/api",
			"io.seagalia.flock.protocol":                 "HTTP2",
			"io.seagalia.flock.backend.port":             "8080",
			"io.seagalia.flock.backend.tls.enable":       "true",
			"io.seagalia.flock.backend.tls.server_name":  "web.internal",
			"io.seagalia.flock.tls.enable":               "true",
			"io.seagalia.flock.tls.redirect":             "true",
			"io.seagalia.flock.lb.policy":                "least_request",
			"io.seagalia.flock.health.path":              "/healthz",
			"io.seagalia.flock.health.interval":          "10s",
			"io.seagalia.flock.health.healthy_threshold": "4",
			"io.seagalia.flock.health.status":            "200-299",
		}})
		Expect(err).NotTo(HaveOccurred())

		Expect(base.Name).To(Equal("shop-web"))
		Expect(*base.Domains).To(Equal(models.Domains{"shop.example.com", "www.example.com"}))
		Expect(*base.Path).To(Equal("/api"))
		Expect(*base.Protocol).To(Equal(models.ProtocolHttp2))
		Expect(*base.Backend.Port).To(Equal(models.Port(8080)))
		Expect(base.Backend.TLS).To(Equal(&models.BackendTLS{Enable: true, ServerName: "web.internal"}))
		Expect(base.TLS).To(Equal(&models.TLS{Enable: true, Redirect: true}))
		Expect(*base.LB.Policy).To(Equal(models.LBPolicy(backendv1.Backend_LEAST_REQUEST)))
		Expect(base.Health.Path).To(Equal("/healthz"))
		Expect(*base.Health.Interval).To(Equal(models.Duration(10 * time.Second)))
		Expect(*base.Health.HealthyThreshold).To(Equal(models.Threshold(4)))
		Expect(*base.Health.Status).To(Equal(models.StatusRange{Start: 200, End: 300}))
	})

	It("reads the external auth labels", func() {
		base, err := models.Decode(container.Summary{ID: "c1", Labels: map[string]string{
			"com.docker.compose.project":                      "shop",
			"com.docker.compose.service":                      "authelia",
			"io.seagalia.flock.enable":                        "true",
			"io.seagalia.flock.auth.policy":                   "other",
			"io.seagalia.flock.auth.provide.name":             "sso",
			"io.seagalia.flock.auth.provide.path":             "/api/authz/forward-auth",
			"io.seagalia.flock.auth.provide.request_headers":  "cookie, accept",
			"io.seagalia.flock.auth.provide.upstream_headers": "remote-user",
			"io.seagalia.flock.auth.provide.client_headers":   "set-cookie",
			"io.seagalia.flock.auth.provide.timeout":          "2s",
			"io.seagalia.flock.auth.provide.failure_mode":     "Allow",
			"io.seagalia.flock.auth.provide.status_on_error":  "401",
		}})
		Expect(err).NotTo(HaveOccurred())

		Expect(base.AuthPolicy()).To(Equal("other"))
		Expect(base.Auth.Provide).To(Equal(&models.AuthProvide{
			Name:            "sso",
			Path:            "/api/authz/forward-auth",
			RequestHeaders:  models.Headers{"cookie", "accept"},
			UpstreamHeaders: models.Headers{"remote-user"},
			ClientHeaders:   models.Headers{"set-cookie"},
			Timeout:         new(models.Duration(2 * time.Second)),
			FailureMode:     new(models.FailureMode(route.FailureMode_ALLOW)),
			StatusOnError:   401,
		}))
	})

	It("answers nil for a container that didn't opt in", func() {
		base, err := models.Decode(container.Summary{ID: "c1", Labels: map[string]string{
			"io.seagalia.flock.domains": "db.example.com",
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(base).To(BeNil())
	})

	It("refuses an enabled container out of compose", func() {
		_, err := models.Decode(container.Summary{ID: "c1", Labels: map[string]string{
			"io.seagalia.flock.enable":   "true",
			"com.docker.compose.project": "shop",
		}})
		Expect(err).To(MatchError(ContainSubstring("com.docker.compose.service")))
	})

	DescribeTable("refuses invalid values",
		func(key, value string) {
			_, err := models.Decode(container.Summary{ID: "c1", Labels: map[string]string{
				"io.seagalia.flock.enable":   "true",
				"com.docker.compose.project": "shop",
				"com.docker.compose.service": "web",
				key:                          value,
			}})
			Expect(err).To(HaveOccurred())
		},
		Entry("protocol", "io.seagalia.flock.protocol", "smtp"),
		Entry("port", "io.seagalia.flock.listen_port", "70000"),
		Entry("lb policy", "io.seagalia.flock.lb.policy", "fastest"),
		Entry("duration", "io.seagalia.flock.health.timeout", "soon"),
		Entry("status range", "io.seagalia.flock.health.status", "399-200"),
		Entry("failure mode", "io.seagalia.flock.auth.provide.failure_mode", "maybe"),
		Entry("status on error", "io.seagalia.flock.auth.provide.status_on_error", "teapot"),
	)
})
