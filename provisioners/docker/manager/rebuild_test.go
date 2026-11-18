package manager

import (
	"context"
	"maps"

	"github.com/moby/moby/api/types/container"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/provisioners/docker/eds"
	"github.com/seagalia/flock/provisioners/docker/models"
	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	edsv1 "github.com/seagalia/vigie/sdk/vigie/eds/v1"
)

const (
	agentUuid        = "agent-1"
	whoami1          = "flock-dev-privileged-whoami-1"
	whoami2          = "flock-dev-privileged-whoami-2"
	defaultHttpVs    = models.DefaultHttpVirtualServer
	defaultHttpsVs   = models.DefaultHttpsVirtualServer
	whoami1Backend   = models.NamePrefix + whoami1
	whoami2Backend   = models.NamePrefix + whoami2
	whoami1Domain    = "whoami-1.example.com"
	tcpVirtualServer = models.NamePrefix + "tcp-5432"
)

type fakeIdentity struct{}

func (fakeIdentity) WaitAgentUuid(context.Context) (string, error) {
	return agentUuid, nil
}

func testConfig() *config.Config {
	return &config.Config{Docker: config.DockerConfig{
		Enabled:   true,
		Vip:       "local",
		HttpPort:  80,
		HttpsPort: 443,
		Eds:       config.EdsConfig{AdvertiseUrl: "grpc://provisioner:18003"},
	}}
}

// newTestManager is a manager past the wait of its uuid, as Run leaves it
func newTestManager(docker *fakeDocker, controller *fakeController, edsServer *eds.Server) *Manager {
	GinkgoHelper()
	cnf := testConfig()
	manager := NewManager(cnf, docker, edsServer, controller.clients(), fakeIdentity{})
	converter, err := models.NewConverter(cnf, agentUuid)
	Expect(err).NotTo(HaveOccurred())
	manager.converter = converter
	manager.agentUuid = agentUuid
	return manager
}

func edsIps(edsServer *eds.Server, service string) []string {
	resp, err := edsServer.GetEndpoints(context.Background(), &edsv1.GetEndpointsRequest{ServiceName: service})
	if err != nil {
		return nil
	}
	ips := []string{}
	for _, endpoint := range resp.GetEndpoints() {
		ips = append(ips, endpoint.GetIp())
	}
	return ips
}

func domainsOf(virtualServer *vs.VirtualServer) [][]string {
	domains := make([][]string, 0, len(virtualServer.GetHttpRoutesConfig().GetVirtualHosts()))
	for _, virtualHost := range virtualServer.GetHttpRoutesConfig().GetVirtualHosts() {
		domains = append(domains, virtualHost.GetDomains())
	}
	return domains
}

func withLabels(containers []container.Summary, service string, labels map[string]string) []container.Summary {
	for _, summary := range containers {
		if summary.Labels["com.docker.compose.service"] != service {
			continue
		}
		maps.Copy(summary.Labels, labels)
	}
	return containers
}

var _ = Describe("rebuild", func() {
	var (
		ctx        context.Context
		docker     *fakeDocker
		controller *fakeController
		edsServer  *eds.Server
		manager    *Manager
		localVip   *vip.VIP
	)

	BeforeEach(func() {
		ctx = context.Background()
		docker = &fakeDocker{}
		docker.setContainers(composeProject())
		controller = newFakeController()
		localVip = controller.vips.put(&vip.VIP{Name: "local", Ip: "0.0.0.0"})
		edsServer = eds.NewServer()
		manager = newTestManager(docker, controller, edsServer)
	})

	vipHolds := func() []string {
		controller.mu.Lock()
		defer controller.mu.Unlock()
		return controller.vips.items[localVip.GetUUID()].GetVirtualServersUuid()
	}

	It("creates the backends and the default http virtual server on the vip, and fills the eds", func() {
		manager.rebuild(ctx)

		Expect(controller.backends.byName(whoami1Backend)).NotTo(BeNil())
		Expect(controller.backends.byName(whoami2Backend)).NotTo(BeNil())
		httpVs := controller.vss.byName(defaultHttpVs)
		Expect(httpVs.GetPort()).To(Equal(uint32(80)))
		Expect(domainsOf(httpVs)).To(ConsistOf([]string{whoami1Domain}, []string{"whoami-2.example.com"}))
		Expect(vipHolds()).To(Equal([]string{httpVs.GetUUID()}))
		Expect(edsIps(edsServer, whoami1)).To(Equal([]string{"172.18.0.2", "172.18.0.3"}))
		Expect(edsIps(edsServer, whoami2)).To(Equal([]string{"172.30.90.22"}))
	})

	It("writes nothing when nothing changed, a replica less only changes the eds", func() {
		manager.rebuild(ctx)
		writes := controller.writeCount()

		manager.rebuild(ctx)
		Expect(controller.writeCount()).To(Equal(writes))

		containers := composeProject()
		docker.setContainers(append(containers[:1:1], containers[2:]...))
		manager.rebuild(ctx)
		Expect(controller.writeCount()).To(Equal(writes))
		Expect(edsIps(edsServer, whoami1)).To(Equal([]string{"172.18.0.2"}))
	})

	It("lists the owned resources once, then trusts the cache until a write fails", func() {
		manager.rebuild(ctx)
		manager.rebuild(ctx)
		docker.setContainers(withoutService(composeProject(), "whoami-2"))
		manager.rebuild(ctx)
		Expect(controller.ownedListCount()).To(Equal(1))
		Expect(controller.backends.byName(whoami2Backend)).To(BeNil())

		controller.backends.put(&backendv1.Backend{Name: whoami2Backend})
		docker.setContainers(composeProject())
		manager.rebuild(ctx)
		Expect(controller.ownedListCount()).To(Equal(1))
		manager.rebuild(ctx)
		Expect(controller.ownedListCount()).To(Equal(2))
	})

	It("sees a change made on the controller once the cache is dropped, as a resync does", func() {
		manager.rebuild(ctx)
		controller.mu.Lock()
		delete(controller.backends.items, controller.backends.byName(whoami1Backend).GetUUID())
		controller.mu.Unlock()

		manager.rebuild(ctx)
		Expect(controller.backends.byName(whoami1Backend)).To(BeNil())

		manager.owned = nil
		manager.rebuild(ctx)
		Expect(controller.backends.byName(whoami1Backend)).NotTo(BeNil())
	})

	It("removes the route and the backend of a gone service, then the virtual server with the last one", func() {
		manager.rebuild(ctx)
		httpVsUuid := controller.vss.byName(defaultHttpVs).GetUUID()

		docker.setContainers(withoutService(composeProject(), "whoami-2"))
		manager.rebuild(ctx)
		Expect(controller.backends.byName(whoami2Backend)).To(BeNil())
		Expect(domainsOf(controller.vss.byName(defaultHttpVs))).To(Equal([][]string{{whoami1Domain}}))
		Expect(edsIps(edsServer, whoami2)).To(BeEmpty())

		docker.setContainers(withoutService(withoutService(composeProject(), "whoami-2"), "whoami-1"))
		manager.rebuild(ctx)
		Expect(controller.vss.byName(defaultHttpVs)).To(BeNil())
		Expect(vipHolds()).NotTo(ContainElement(httpVsUuid))
		Expect(controller.backends.all()).To(BeEmpty())
	})

	It("orders a certificate for tls on the default https virtual server, and keeps it once the service is gone", func() {
		docker.setContainers(withLabels(composeProject(), "whoami-1", map[string]string{
			"io.seagalia.flock.tls.enable":   "true",
			"io.seagalia.flock.tls.redirect": "true",
		}))
		manager.rebuild(ctx)

		certificate := controller.certs.byName(whoami1Backend)
		Expect(certificate.GetDomainNames()).To(Equal([]string{whoami1Domain}))
		Expect(certificate.GetMetadata()).To(HaveKeyWithValue("flock.provisioner.acme", "true"))
		httpsVs := controller.vss.byName(defaultHttpsVs)
		Expect(httpsVs.GetPort()).To(Equal(uint32(443)))
		Expect(httpsVs.GetHttpRoutesConfig().GetVirtualHosts()[0].GetTlsOptions()[0].GetCertificatesUuid()).
			To(Equal([]string{certificate.GetUUID()}))
		httpVs := controller.vss.byName(defaultHttpVs)
		redirect := findRoute(httpVs, whoami1Domain, whoami1)
		Expect(redirect.GetRedirect().GetHttpsRedirect()).To(BeTrue())
		Expect(vipHolds()).To(ConsistOf(httpVs.GetUUID(), httpsVs.GetUUID()))

		docker.setContainers(withoutService(composeProject(), "whoami-1"))
		manager.rebuild(ctx)
		Expect(controller.vss.byName(defaultHttpsVs)).To(BeNil())
		Expect(findRoute(controller.vss.byName(defaultHttpVs), whoami1Domain, whoami1)).To(BeNil())
		Expect(controller.certs.byName(whoami1Backend)).NotTo(BeNil())
	})

	It("gives a tcp service its own virtual server on the vip, removed with the service", func() {
		docker.setContainers(withLabels(composeProject(), "whoami-2", map[string]string{
			"io.seagalia.flock.protocol":    "tcp",
			"io.seagalia.flock.listen_port": "5432",
		}))
		manager.rebuild(ctx)

		tcpVs := controller.vss.byName(tcpVirtualServer)
		Expect(tcpVs.GetTcpRouteConfig().GetBackendUuid()).To(Equal(controller.backends.byName(whoami2Backend).GetUUID()))
		Expect(vipHolds()).To(ContainElement(tcpVs.GetUUID()))

		docker.setContainers(withoutService(composeProject(), "whoami-2"))
		manager.rebuild(ctx)
		Expect(controller.vss.byName(tcpVirtualServer)).To(BeNil())
		Expect(vipHolds()).NotTo(ContainElement(tcpVs.GetUUID()))
		Expect(controller.backends.byName(whoami2Backend)).To(BeNil())
	})

	It("shares one TLS virtual server between the tcp services with TLS on a port, routed on their domains", func() {
		tlsOnPort := map[string]string{
			"io.seagalia.flock.protocol":    "tcp",
			"io.seagalia.flock.listen_port": "5432",
		}
		containers := withLabels(composeProject(), "whoami-1", tlsOnPort)
		containers = withLabels(containers, "whoami-1", map[string]string{"io.seagalia.flock.tls.enable": "true"})
		containers = withLabels(containers, "whoami-2", tlsOnPort)
		containers = withLabels(containers, "whoami-2", map[string]string{"io.seagalia.flock.tls.passthrough": "true"})
		docker.setContainers(containers)
		manager.rebuild(ctx)

		tlsVs := controller.vss.byName(models.NamePrefix + "tls-5432")
		Expect(tlsVs.GetPort()).To(Equal(uint32(5432)))
		routes := tlsVs.GetTlsTcpRoutesConfig().GetRoutes()
		Expect(routes).To(HaveLen(2))
		certificate := controller.certs.byName(whoami1Backend)
		Expect(certificate.GetDomainNames()).To(Equal([]string{whoami1Domain}))
		Expect(routes[0].GetTls().GetCertificatesUuid()).To(Equal([]string{certificate.GetUUID()}))
		Expect(routes[1].GetPassthrough().GetServerNames()).To(Equal([]string{"whoami-2.example.com"}))
		Expect(controller.certs.byName(whoami2Backend)).To(BeNil())
		Expect(vipHolds()).To(Equal([]string{tlsVs.GetUUID()}))
		writes := controller.writeCount()
		manager.rebuild(ctx)
		Expect(controller.writeCount()).To(Equal(writes))

		docker.setContainers(withoutService(containers, "whoami-2"))
		manager.rebuild(ctx)
		routes = controller.vss.byName(models.NamePrefix + "tls-5432").GetTlsTcpRoutesConfig().GetRoutes()
		Expect(routes).To(HaveLen(1))
		Expect(routes[0].GetBackendUuid()).To(Equal(controller.backends.byName(whoami1Backend).GetUUID()))
		Expect(controller.backends.byName(whoami2Backend)).To(BeNil())

		docker.setContainers(withoutService(withoutService(containers, "whoami-2"), "whoami-1"))
		manager.rebuild(ctx)
		Expect(controller.vss.byName(models.NamePrefix + "tls-5432")).To(BeNil())
		Expect(vipHolds()).To(BeEmpty())
		Expect(controller.certs.byName(whoami1Backend)).NotTo(BeNil())
	})

	It("joins a virtual server of a user named by label, and leaves it its own virtual hosts", func() {
		front := controller.vss.put(&vs.VirtualServer{
			Name: "front",
			Port: 8080,
			RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{
				VirtualHosts: []*route.VirtualHost{{
					Domains: []string{"shop.example.com"},
					Routes: []*route.Route{{
						Name:   "shop",
						Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
						Action: &route.Route_BackendUuid{BackendUuid: "backend-of-user"},
					}},
				}},
			}},
		})
		docker.setContainers(withLabels(composeProject(), "whoami-2", map[string]string{
			"io.seagalia.flock.virtual_server": "front",
		}))
		manager.rebuild(ctx)
		Expect(domainsOf(controller.vss.byName("front"))).To(ConsistOf(
			[]string{"shop.example.com"}, []string{"whoami-2.example.com"},
		))
		Expect(vipHolds()).NotTo(ContainElement(front.GetUUID()))

		docker.setContainers(withoutService(composeProject(), "whoami-2"))
		manager.rebuild(ctx)
		Expect(domainsOf(controller.vss.byName("front"))).To(Equal([][]string{{"shop.example.com"}}))
		Expect(controller.backends.byName(whoami2Backend)).To(BeNil())
	})

	It("moves the route of a service whose domains changed, leaving no copy on the old domains", func() {
		manager.rebuild(ctx)

		docker.setContainers(withLabels(composeProject(), "whoami-1", map[string]string{
			"io.seagalia.flock.domains": "renamed.example.com",
		}))
		manager.rebuild(ctx)

		Expect(domainsOf(controller.vss.byName(defaultHttpVs))).To(ConsistOf(
			[]string{"renamed.example.com"}, []string{"whoami-2.example.com"},
		))
	})

	It("moves the route of a service to the https virtual server once TLS is on", func() {
		manager.rebuild(ctx)

		docker.setContainers(withLabels(composeProject(), "whoami-1", map[string]string{
			"io.seagalia.flock.tls.enable": "true",
		}))
		manager.rebuild(ctx)

		Expect(domainsOf(controller.vss.byName(defaultHttpVs))).To(Equal([][]string{{"whoami-2.example.com"}}))
		Expect(domainsOf(controller.vss.byName(defaultHttpsVs))).To(Equal([][]string{{whoami1Domain}}))
	})

	It("takes the route of a running service out of the virtual server of a user it no longer names", func() {
		controller.vss.put(&vs.VirtualServer{
			Name: "front",
			Port: 8080,
			RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{
				VirtualHosts: []*route.VirtualHost{{
					Domains: []string{"shop.example.com"},
					Routes: []*route.Route{{
						Name:   "shop",
						Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
						Action: &route.Route_BackendUuid{BackendUuid: "backend-of-user"},
					}},
				}},
			}},
		})
		docker.setContainers(withLabels(composeProject(), "whoami-2", map[string]string{
			"io.seagalia.flock.virtual_server": "front",
		}))
		manager.rebuild(ctx)

		docker.setContainers(composeProject())
		manager.rebuild(ctx)

		Expect(domainsOf(controller.vss.byName("front"))).To(Equal([][]string{{"shop.example.com"}}))
		Expect(domainsOf(controller.vss.byName(defaultHttpVs))).To(ConsistOf(
			[]string{whoami1Domain}, []string{"whoami-2.example.com"},
		))
	})

	Describe("external auth", func() {
		var authProject []container.Summary

		BeforeEach(func() {
			authProject = withLabels(composeProject(), "whoami-1", map[string]string{
				"io.seagalia.flock.auth.provide.name":             "sso",
				"io.seagalia.flock.auth.provide.path":             "/api/authz/forward-auth",
				"io.seagalia.flock.auth.provide.request_headers":  "Cookie",
				"io.seagalia.flock.auth.provide.upstream_headers": "remote-user",
			})
			authProject = withLabels(authProject, "whoami-2", map[string]string{"io.seagalia.flock.auth.policy": "sso"})
		})

		It("copies the policy of the provider into the virtual server of the route naming it", func() {
			docker.setContainers(authProject)
			manager.rebuild(ctx)

			httpVs := controller.vss.byName(defaultHttpVs)
			policies := httpVs.GetHttpRoutesConfig().GetExternalAuths()
			Expect(policies).To(HaveLen(1))
			Expect(policies[0].GetName()).To(Equal("sso"))
			Expect(policies[0].GetBackendUuid()).To(Equal(controller.backends.byName(whoami1Backend).GetUUID()))
			Expect(policies[0].GetPath()).To(Equal("/api/authz/forward-auth"))
			Expect(policies[0].GetRequestHeaders()).To(Equal([]string{"cookie"}))
			Expect(findRoute(httpVs, "whoami-2.example.com", whoami2).GetExternalAuthName()).To(Equal("sso"))
			Expect(findRoute(httpVs, whoami1Domain, whoami1).GetExternalAuthName()).To(BeEmpty())
			writes := controller.writeCount()
			manager.rebuild(ctx)
			Expect(controller.writeCount()).To(Equal(writes))
		})

		It("drops the policy once no route names it", func() {
			docker.setContainers(authProject)
			manager.rebuild(ctx)

			docker.setContainers(withLabels(authProject, "whoami-2", map[string]string{"io.seagalia.flock.auth.policy": ""}))
			manager.rebuild(ctx)
			httpVs := controller.vss.byName(defaultHttpVs)
			Expect(httpVs.GetHttpRoutesConfig().GetExternalAuths()).To(BeEmpty())
			Expect(findRoute(httpVs, "whoami-2.example.com", whoami2).GetExternalAuthName()).To(BeEmpty())
		})

		It("stops serving the routes of a gone provider, then deletes its backend", func() {
			docker.setContainers(authProject)
			manager.rebuild(ctx)

			docker.setContainers(withoutService(authProject, "whoami-1"))
			manager.rebuild(ctx)
			Expect(controller.vss.byName(defaultHttpVs)).To(BeNil())
			Expect(controller.backends.byName(whoami1Backend)).To(BeNil())
		})

		It("leaves out a route naming a policy no container provides, and serves the others", func() {
			docker.setContainers(withLabels(composeProject(), "whoami-2", map[string]string{
				"io.seagalia.flock.auth.policy": "missing",
			}))
			manager.rebuild(ctx)

			Expect(domainsOf(controller.vss.byName(defaultHttpVs))).To(Equal([][]string{{whoami1Domain}}))
		})

		It("keeps the policies of a user in the virtual server it joins, and never overwrites one of the same name", func() {
			userPolicy := func(name string) *route.ExternalAuth {
				return &route.ExternalAuth{Name: name, BackendUuid: "backend-of-user", Path: "/check"}
			}
			controller.vss.put(&vs.VirtualServer{
				Name: "front",
				Port: 8080,
				RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{
					VirtualHosts: []*route.VirtualHost{{
						Domains: []string{"shop.example.com"},
						Routes: []*route.Route{{
							Name:   "shop",
							Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
							Action: &route.Route_BackendUuid{BackendUuid: "backend-of-user"},
						}},
					}},
					ExternalAuths: []*route.ExternalAuth{userPolicy("user-sso")},
				}},
			})
			docker.setContainers(withLabels(authProject, "whoami-2", map[string]string{
				"io.seagalia.flock.virtual_server": "front",
			}))
			manager.rebuild(ctx)

			front := controller.vss.byName("front")
			policyNames := make([]string, 0, len(front.GetHttpRoutesConfig().GetExternalAuths()))
			for _, policy := range front.GetHttpRoutesConfig().GetExternalAuths() {
				policyNames = append(policyNames, policy.GetName())
			}
			Expect(policyNames).To(Equal([]string{"user-sso", "sso"}))
			Expect(findRoute(front, "whoami-2.example.com", whoami2).GetExternalAuthName()).To(Equal("sso"))

			docker.setContainers(withoutService(authProject, "whoami-2"))
			manager.rebuild(ctx)
			front = controller.vss.byName("front")
			Expect(front.GetHttpRoutesConfig().GetExternalAuths()).To(HaveLen(1))
			Expect(front.GetHttpRoutesConfig().GetExternalAuths()[0].GetName()).To(Equal("user-sso"))

			front.GetHttpRoutesConfig().ExternalAuths = append(front.GetHttpRoutesConfig().ExternalAuths, userPolicy("sso"))
			controller.vss.put(front)
			docker.setContainers(withLabels(authProject, "whoami-2", map[string]string{
				"io.seagalia.flock.virtual_server": "front",
			}))
			manager.rebuild(ctx)
			front = controller.vss.byName("front")
			Expect(domainsOf(front)).To(Equal([][]string{{"shop.example.com"}}))
			Expect(front.GetHttpRoutesConfig().GetExternalAuths()[1].GetBackendUuid()).To(Equal("backend-of-user"))
		})
	})

	It("leaves a service out when a resource of a user has its name, and goes on with the others", func() {
		controller.backends.put(&backendv1.Backend{Name: whoami2Backend})

		manager.rebuild(ctx)
		Expect(controller.backends.byName(whoami1Backend)).NotTo(BeNil())
		Expect(controller.backends.byName(whoami2Backend).GetMetadata()).To(BeEmpty())
		Expect(domainsOf(controller.vss.byName(defaultHttpVs))).To(Equal([][]string{{whoami1Domain}}))
	})
})

func findRoute(virtualServer *vs.VirtualServer, domain, name string) *route.Route {
	for _, virtualHost := range virtualServer.GetHttpRoutesConfig().GetVirtualHosts() {
		for _, r := range virtualHost.GetRoutes() {
			isSought := r.GetName() == name && virtualHost.GetDomains()[0] == domain
			if isSought {
				return r
			}
		}
	}
	return nil
}
