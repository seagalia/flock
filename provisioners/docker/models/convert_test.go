package models_test

import (
	"maps"
	"os"
	"path/filepath"

	"github.com/moby/moby/api/types/container"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/provisioners/docker/models"
	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_commopts "github.com/seagalia/flock/sdk/flock/extensions/common/starling_options/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

// decode answers the base of the compose service named service in the project shop
func decode(service string, labels map[string]string) *models.Base {
	GinkgoHelper()
	labels["io.seagalia.flock.enable"] = "true"
	labels["com.docker.compose.project"] = "shop"
	labels["com.docker.compose.service"] = service
	base, err := models.Decode(container.Summary{ID: service, Labels: labels})
	Expect(err).NotTo(HaveOccurred())
	return base
}

func routeNames(virtualHost *route.VirtualHost) []string {
	names := make([]string, 0, len(virtualHost.GetRoutes()))
	for _, r := range virtualHost.GetRoutes() {
		names = append(names, r.GetName())
	}
	return names
}

var _ = Describe("Converter", func() {
	var (
		converter *models.Converter
		cnf       *config.Config
	)

	BeforeEach(func() {
		caFile := filepath.Join(GinkgoT().TempDir(), "ca.pem")
		Expect(os.WriteFile(caFile, []byte("ca pem"), 0600)).To(Succeed())
		cnf = &config.Config{
			TlsConfig: &agentconfig.TlsConfig{CaFile: caFile},
			Docker: config.DockerConfig{
				Enabled:   true,
				HttpPort:  8080,
				HttpsPort: 8443,
				Eds:       config.EdsConfig{AdvertiseUrl: "grpcs://provisioner:18003"},
			},
		}
		var err error
		converter, err = models.NewConverter(cnf, "agent-1")
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("NewConverter", func() {
		It("leaves the eds ca empty when the provisioner has no ca, for the system CAs", func() {
			cnf.TlsConfig.CaFile = ""
			converter, err := models.NewConverter(cnf, "agent-1")
			Expect(err).NotTo(HaveOccurred())

			backend, err := converter.Backend(decode("web", map[string]string{}))
			Expect(err).NotTo(HaveOccurred())
			Expect(backend.GetEds().GetCa()).To(BeEmpty())
		})

		It("fails on a ca file it can't read", func() {
			cnf.TlsConfig.CaFile = filepath.Join(GinkgoT().TempDir(), "missing.pem")
			_, err := models.NewConverter(cnf, "agent-1")
			Expect(err).To(MatchError(ContainSubstring("read the eds ca")))
		})
	})

	Describe("Backend", func() {
		It("reads the endpoints of the container from the eds of the provisioner", func() {
			backend, err := converter.Backend(decode("web", map[string]string{}))
			Expect(err).NotTo(HaveOccurred())
			Expect(helpers.Validate(backend)).To(Succeed())

			Expect(backend.GetName()).To(Equal("io.seagalia.flock.docker/shop-web"))
			Expect(backend.GetEds().GetServiceName()).To(Equal("shop-web"))
			Expect(backend.GetEds().GetUrl().GetScheme()).To(Equal("grpcs"))
			Expect(backend.GetEds().GetUrl().GetHost()).To(Equal("provisioner"))
			Expect(backend.GetEds().GetUrl().GetPort()).To(Equal(uint32(18003)))
			Expect(backend.GetEds().GetCa()).To(Equal("ca pem"))
			Expect(backend.GetLbPolicy()).To(Equal(backendv1.Backend_ROUND_ROBIN))
			Expect(backend.GetHealthCheck().GetNoHealthCheck()).NotTo(BeNil())
			Expect(backend.GetMetadata()).To(Equal(map[string]string{
				"flock.autoprovision":      "true",
				"flock.provision.by":       "agent-1",
				"flock.provisioner.docker": "true",
			}))
		})

		It("applies the backend, lb, health and http2 labels", func() {
			backend, err := converter.Backend(decode("web", map[string]string{
				"io.seagalia.flock.protocol":                "http2",
				"io.seagalia.flock.backend.tls.enable":      "true",
				"io.seagalia.flock.backend.tls.server_name": "web.internal",
				"io.seagalia.flock.lb.policy":               "maglev",
				"io.seagalia.flock.health.path":             "/healthz",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(helpers.Validate(backend)).To(Succeed())

			Expect(backend.GetEnableTls()).To(BeTrue())
			Expect(backend.GetSni()).To(Equal("web.internal"))
			Expect(backend.GetLbPolicy()).To(Equal(backendv1.Backend_MAGLEV))
			httpCheck := backend.GetHealthCheck().GetHttpHealthCheck()
			Expect(httpCheck.GetPath()).To(Equal("/healthz"))
			Expect(httpCheck.GetExpectedStatuses().GetStart()).To(Equal(int64(200)))
			Expect(httpCheck.GetExpectedStatuses().GetEnd()).To(Equal(int64(400)))

			options := &starling_commopts.HttpProtocolOptions{}
			Expect(backend.GetHttpProtocolOptions().UnmarshalTo(options)).To(Succeed())
			Expect(options.GetExplicitHttpConfig().GetHttp2ProtocolOptions()).NotTo(BeNil())
		})
	})

	Describe("Certificate", func() {
		It("asks the acme provisioner for the domains", func() {
			certificate, err := converter.Certificate(decode("web", map[string]string{
				"io.seagalia.flock.tls.enable": "true",
				"io.seagalia.flock.domains":    "shop.example.com,*.shop.example.com",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(helpers.Validate(certificate)).To(Succeed())
			Expect(certificate.GetName()).To(Equal("io.seagalia.flock.docker/shop-web"))
			Expect(certificate.GetDomainNames()).To(Equal([]string{"shop.example.com", "*.shop.example.com"}))
			Expect(certificate.GetMetadata()).To(HaveKeyWithValue("flock.provisioner.acme", "true"))
			Expect(certificate.GetMetadata()).To(HaveKeyWithValue("flock.autoprovision", "true"))
		})

		It("makes none when tls is off or a certificate is given", func() {
			certificate, err := converter.Certificate(decode("web", map[string]string{}))
			Expect(err).NotTo(HaveOccurred())
			Expect(certificate).To(BeNil())

			certificate, err = converter.Certificate(decode("web", map[string]string{
				"io.seagalia.flock.tls.enable": "true",
				"io.seagalia.flock.tls.cert":   "shop-cert",
			}))
			Expect(err).NotTo(HaveOccurred())
			Expect(certificate).To(BeNil())
		})

		It("refuses to order a certificate for every domain", func() {
			_, err := converter.Certificate(decode("web", map[string]string{
				"io.seagalia.flock.tls.enable": "true",
				"io.seagalia.flock.domains":    "*",
			}))
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("http virtual hosts", func() {
		It("joins the default http virtual server on the name of its service by default", func() {
			base := decode("web", map[string]string{})
			virtualHost, err := converter.VirtualHost(base, "backend-1", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(converter.JoinedVirtualServerRef(base)).To(Equal(models.DefaultHttpVirtualServer))

			virtualServer, err := converter.DefaultVirtualServer(models.DefaultHttpVirtualServer, virtualHost)
			Expect(err).NotTo(HaveOccurred())
			Expect(helpers.Validate(virtualServer)).To(Succeed())
			Expect(virtualServer.GetName()).To(Equal("io.seagalia.flock.docker/http"))
			Expect(virtualServer.GetPort()).To(Equal(uint32(8080)))
			Expect(virtualHost.GetDomains()).To(Equal([]string{"web"}))
			Expect(virtualHost.GetRoutes()[0].GetMatch().GetPrefix()).To(Equal("/"))
			Expect(virtualHost.GetRoutes()[0].GetBackendUuid()).To(Equal("backend-1"))
		})

		It("gives a service with no domain label <service>.<default_domain>, and orders its certificate", func() {
			cnf.Docker.DefaultDomain = "apps.example.com"
			withDefault, err := models.NewConverter(cnf, "agent-1")
			Expect(err).NotTo(HaveOccurred())
			base := decode("web", map[string]string{"io.seagalia.flock.tls.enable": "true"})

			virtualHost, err := withDefault.VirtualHost(base, "backend-1", "cert-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(virtualHost.GetDomains()).To(Equal([]string{"web.apps.example.com"}))
			certificate, err := withDefault.Certificate(base)
			Expect(err).NotTo(HaveOccurred())
			Expect(certificate.GetDomainNames()).To(Equal([]string{"web.apps.example.com"}))

			labelled := decode("web", map[string]string{"io.seagalia.flock.domains": "shop.example.com"})
			virtualHost, err = withDefault.VirtualHost(labelled, "backend-1", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(virtualHost.GetDomains()).To(Equal([]string{"shop.example.com"}))
		})

		It("keeps the wildcard domain as the virtual host of every domain", func() {
			virtualHost, err := converter.VirtualHost(decode("web", map[string]string{"io.seagalia.flock.domains": "*"}), "backend-1", "cert-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(virtualHost.GetDomains()).To(Equal([]string{"*"}))
			Expect(virtualHost.GetTlsOptions()[0].GetServerNames()).To(BeEmpty())
		})

		It("terminates tls on the default https virtual server and redirects plain http", func() {
			base := decode("web", map[string]string{
				"io.seagalia.flock.tls.enable":   "true",
				"io.seagalia.flock.tls.redirect": "true",
				"io.seagalia.flock.domains":      "shop.example.com",
			})
			virtualHost, err := converter.VirtualHost(base, "backend-1", "cert-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(converter.JoinedVirtualServerRef(base)).To(Equal(models.DefaultHttpsVirtualServer))
			Expect(virtualHost.GetTlsOptions()).To(HaveLen(1))
			Expect(virtualHost.GetTlsOptions()[0].GetServerNames()).To(Equal([]string{"shop.example.com"}))
			Expect(virtualHost.GetTlsOptions()[0].GetCertificatesUuid()).To(Equal([]string{"cert-1"}))

			virtualServer, err := converter.DefaultVirtualServer(models.DefaultHttpsVirtualServer, virtualHost)
			Expect(err).NotTo(HaveOccurred())
			Expect(helpers.Validate(virtualServer)).To(Succeed())
			Expect(virtualServer.GetName()).To(Equal("io.seagalia.flock.docker/https"))
			Expect(virtualServer.GetPort()).To(Equal(uint32(8443)))
			Expect(virtualServer.GetMetadata()).To(HaveKeyWithValue("flock.autoprovision", "true"))

			redirect, err := converter.RedirectVirtualHost(base)
			Expect(err).NotTo(HaveOccurred())
			Expect(redirect.GetRoutes()[0].GetRedirect().GetHttpsRedirect()).To(BeTrue())
		})

		It("never creates a virtual server named by a label", func() {
			base := decode("web", map[string]string{"io.seagalia.flock.virtual_server": "front"})
			Expect(converter.JoinedVirtualServerRef(base)).To(Equal("front"))
			_, err := converter.DefaultVirtualServer("front", &route.VirtualHost{})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("JoinVirtualHost and RemoveRoutes", func() {
		var stored *vs.VirtualServer

		BeforeEach(func() {
			root, err := converter.VirtualHost(decode("root", map[string]string{"io.seagalia.flock.domains": "shop.example.com"}), "backend-root", "")
			Expect(err).NotTo(HaveOccurred())
			stored, err = converter.DefaultVirtualServer(models.DefaultHttpVirtualServer, root)
			Expect(err).NotTo(HaveOccurred())
		})

		It("adds the routes to the virtual host of the same domains, longest prefix first", func() {
			api, err := converter.VirtualHost(decode("api", map[string]string{
				"io.seagalia.flock.domains": "shop.example.com",
				"io.seagalia.flock.path":    "/api",
			}), "backend-api", "")
			Expect(err).NotTo(HaveOccurred())

			joined, err := models.JoinVirtualHost(stored, api)
			Expect(err).NotTo(HaveOccurred())
			Expect(helpers.Validate(joined)).To(Succeed())
			virtualHosts := joined.GetHttpRoutesConfig().GetVirtualHosts()
			Expect(virtualHosts).To(HaveLen(1))
			Expect(routeNames(virtualHosts[0])).To(Equal([]string{"shop-api", "shop-root"}))
			Expect(routeNames(stored.GetHttpRoutesConfig().GetVirtualHosts()[0])).To(Equal([]string{"shop-root"}))
		})

		It("replaces the route of the same name", func() {
			moved, err := converter.VirtualHost(decode("root", map[string]string{"io.seagalia.flock.domains": "shop.example.com"}), "backend-new", "")
			Expect(err).NotTo(HaveOccurred())

			joined, err := models.JoinVirtualHost(stored, moved)
			Expect(err).NotTo(HaveOccurred())
			routes := joined.GetHttpRoutesConfig().GetVirtualHosts()[0].GetRoutes()
			Expect(routes).To(HaveLen(1))
			Expect(routes[0].GetBackendUuid()).To(Equal("backend-new"))
		})

		It("adds a virtual host for other domains and removes it with its last route", func() {
			blog, err := converter.VirtualHost(decode("blog", map[string]string{"io.seagalia.flock.domains": "blog.example.com"}), "backend-blog", "")
			Expect(err).NotTo(HaveOccurred())

			joined, err := models.JoinVirtualHost(stored, blog)
			Expect(err).NotTo(HaveOccurred())
			Expect(helpers.Validate(joined)).To(Succeed())
			Expect(joined.GetHttpRoutesConfig().GetVirtualHosts()).To(HaveLen(2))

			cleaned := models.RemoveRoutes(joined, "shop-blog")
			Expect(cleaned.GetHttpRoutesConfig().GetVirtualHosts()).To(HaveLen(1))
			Expect(cleaned.GetHttpRoutesConfig().GetVirtualHosts()[0].GetDomains()).To(Equal([]string{"shop.example.com"}))
		})

		It("refuses to join a virtual server that is not http", func() {
			tcp := &vs.VirtualServer{Name: "db", RouteConfig: &vs.VirtualServer_TcpRouteConfig{TcpRouteConfig: &vs.TcpRouteConfig{}}}
			_, err := models.JoinVirtualHost(tcp, &route.VirtualHost{})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("external auth", func() {
		provider := func(labels map[string]string) *models.Base {
			named := map[string]string{"io.seagalia.flock.auth.provide.name": "sso"}
			maps.Copy(named, labels)
			return decode("authelia", named)
		}

		It("makes the policy of a provider, with the defaults of the controller", func() {
			policy, err := converter.ExternalAuth(provider(map[string]string{
				"io.seagalia.flock.auth.provide.request_headers": "Cookie",
			}), "backend-auth")
			Expect(err).NotTo(HaveOccurred())
			Expect(policy.GetName()).To(Equal("sso"))
			Expect(policy.GetBackendUuid()).To(Equal("backend-auth"))
			Expect(policy.GetPath()).To(Equal("/"))
			Expect(policy.GetRequestHeaders()).To(Equal([]string{"cookie"}))
			Expect(policy.GetStatusOnError()).To(Equal(uint32(503)))
			Expect(policy.GetFailureMode()).To(Equal(route.FailureMode_DENY))
		})

		It("makes none for a container that provides nothing", func() {
			policy, err := converter.ExternalAuth(decode("web", map[string]string{}), "backend-web")
			Expect(err).NotTo(HaveOccurred())
			Expect(policy).To(BeNil())
		})

		DescribeTable("refuses an invalid provider",
			func(labels map[string]string) {
				_, err := converter.ExternalAuth(provider(labels), "backend-auth")
				Expect(err).To(HaveOccurred())
			},
			Entry("name", map[string]string{"io.seagalia.flock.auth.provide.name": "SSO!"}),
			Entry("path", map[string]string{"io.seagalia.flock.auth.provide.path": "check"}),
			Entry("upstream header", map[string]string{"io.seagalia.flock.auth.provide.upstream_headers": "host"}),
			Entry("status on error", map[string]string{"io.seagalia.flock.auth.provide.status_on_error": "200"}),
			Entry("tcp", map[string]string{"io.seagalia.flock.protocol": "tcp"}),
		)

		It("names the policy on the route, never on the https redirect", func() {
			base := decode("web", map[string]string{
				"io.seagalia.flock.auth.policy":  "sso",
				"io.seagalia.flock.tls.enable":   "true",
				"io.seagalia.flock.tls.redirect": "true",
			})
			virtualHost, err := converter.VirtualHost(base, "backend-web", "cert-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(virtualHost.GetRoutes()[0].GetExternalAuthName()).To(Equal("sso"))

			redirect, err := converter.RedirectVirtualHost(base)
			Expect(err).NotTo(HaveOccurred())
			Expect(redirect.GetRoutes()[0].GetExternalAuth()).To(BeNil())
		})

		It("refuses a policy on a tcp or udp route", func() {
			_, err := converter.VirtualServer(decode("db", map[string]string{
				"io.seagalia.flock.protocol":    "udp",
				"io.seagalia.flock.listen_port": "53",
				"io.seagalia.flock.auth.policy": "sso",
			}), "backend-db")
			Expect(err).To(MatchError(ContainSubstring("auth.policy")))

			_, err = converter.TlsTcpRoute(decode("db", map[string]string{
				"io.seagalia.flock.protocol":        "tcp",
				"io.seagalia.flock.tls.passthrough": "true",
				"io.seagalia.flock.auth.policy":     "sso",
			}), "backend-db", "")
			Expect(err).To(MatchError(ContainSubstring("auth.policy")))
		})

		Describe("JoinExternalAuth and RemoveExternalAuths", func() {
			var stored *vs.VirtualServer

			BeforeEach(func() {
				web, err := converter.VirtualHost(decode("web", map[string]string{}), "backend-web", "")
				Expect(err).NotTo(HaveOccurred())
				stored, err = converter.DefaultVirtualServer(models.DefaultHttpVirtualServer, web)
				Expect(err).NotTo(HaveOccurred())
				stored.GetHttpRoutesConfig().ExternalAuths = []*route.ExternalAuth{
					{Name: "user-sso", BackendUuid: "backend-user", Path: "/"},
				}
			})

			It("adds a policy, then replaces it on the same backend", func() {
				joined, err := models.JoinExternalAuth(stored, &route.ExternalAuth{Name: "sso", BackendUuid: "backend-auth", Path: "/"})
				Expect(err).NotTo(HaveOccurred())
				joined, err = models.JoinExternalAuth(joined, &route.ExternalAuth{Name: "sso", BackendUuid: "backend-auth", Path: "/check"})
				Expect(err).NotTo(HaveOccurred())

				policies := joined.GetHttpRoutesConfig().GetExternalAuths()
				Expect(policies).To(HaveLen(2))
				Expect(policies[1].GetPath()).To(Equal("/check"))
				Expect(stored.GetHttpRoutesConfig().GetExternalAuths()).To(HaveLen(1))

				cleaned := models.RemoveExternalAuths(joined, "backend-auth")
				Expect(cleaned.GetHttpRoutesConfig().GetExternalAuths()).To(HaveLen(1))
				Expect(cleaned.GetHttpRoutesConfig().GetExternalAuths()[0].GetName()).To(Equal("user-sso"))
			})

			It("never overwrites a policy of the same name on another backend", func() {
				_, err := models.JoinExternalAuth(stored, &route.ExternalAuth{Name: "user-sso", BackendUuid: "backend-auth", Path: "/"})
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("tcp and udp virtual servers", func() {
		DescribeTable("make a virtual server of their own on the listen port",
			func(protocol string, check func(*vs.VirtualServer)) {
				virtualServer, err := converter.VirtualServer(decode("db", map[string]string{
					"io.seagalia.flock.protocol":    protocol,
					"io.seagalia.flock.listen_port": "5432",
				}), "backend-db")
				Expect(err).NotTo(HaveOccurred())
				Expect(helpers.Validate(virtualServer)).To(Succeed())
				Expect(virtualServer.GetName()).To(Equal("io.seagalia.flock.docker/" + protocol + "-5432"))
				Expect(virtualServer.GetPort()).To(Equal(uint32(5432)))
				check(virtualServer)
			},
			Entry("tcp", "tcp", func(v *vs.VirtualServer) {
				Expect(v.GetTcpRouteConfig().GetBackendUuid()).To(Equal("backend-db"))
			}),
			Entry("udp", "udp", func(v *vs.VirtualServer) {
				Expect(v.GetUdpRouteConfig().GetBackendUuid()).To(Equal("backend-db"))
			}),
		)

		It("needs a listen port", func() {
			_, err := converter.VirtualServer(decode("db", map[string]string{"io.seagalia.flock.protocol": "tcp"}), "backend-db")
			Expect(err).To(HaveOccurred())
		})

		It("is never made for http", func() {
			_, err := converter.VirtualServer(decode("web", map[string]string{"io.seagalia.flock.listen_port": "80"}), "backend-web")
			Expect(err).To(HaveOccurred())
		})
	})
})
