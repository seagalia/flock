package serves_test

import (
	"context"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("VirtualServer", func() {
	var server *testServer
	var backendUuid string
	vsType := helpers.TypeUrl[*vs.VirtualServer]()
	BeforeEach(func() {
		server = newTestServer(instantiaters.NewNull())
		resp, err := server.CreateBackend(userCtx("bob"), &svcbackend.CreateBackendRequest{Backend: newBackend("backend-1")})
		Expect(err).ToNot(HaveOccurred())
		backendUuid = resp.GetBackend().GetUUID()
	})

	Context("CreateVS", func() {
		It("should create virtual server owned by user", func() {
			resp, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: newVs("vs-1", 80, backendUuid)})
			Expect(err).ToNot(HaveOccurred())

			Expect(server.perm.HasPermission(context.Background(), vsType, resp.GetVs().GetUUID(), permission.Role_OWNER, "bob", nil)).To(BeTrue())
		})
		It("should refuse unknown backend", func() {
			_, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: newVs("vs-1", 80, "unknown")})
			expectCode(err, connect.CodeNotFound)
		})
		It("should create an http virtual server routing its virtual hosts to backends", func() {
			vhost := newVirtualHost(backendUuid)
			resp, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: newHttpVs("vs-http", 80, vhost)})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetVs().GetBackendUuids()).To(Equal([]string{backendUuid}))
		})
		It("should refuse an http virtual server routing to an unknown backend", func() {
			_, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: newHttpVs("vs-http", 80, newVirtualHost("unknown"))})
			expectCode(err, connect.CodeNotFound)
		})
		It("should refuse two virtual hosts with the same domain", func() {
			first := newVirtualHost(backendUuid)
			second := newVirtualHost(backendUuid)
			second.Domains = []string{"a.local", "*"}

			_, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: newHttpVs("vs-http", 80, first, second)})

			expectCode(err, connect.CodeInvalidArgument)
			Expect(err).To(MatchError(ContainSubstring("domain * is in virtual hosts 0 and 1")))
		})
		Context("external auth policies", func() {
			httpVsWithPolicies := func(vhost *route.VirtualHost, policies ...*route.ExternalAuth) *vs.VirtualServer {
				virtServ := newHttpVs("vs-http", 80, vhost)
				virtServ.GetHttpRoutesConfig().ExternalAuths = policies
				return virtServ
			}
			policy := func(name, policyBackendUuid string) *route.ExternalAuth { //nolint:unparam // the call sites show the value
				return &route.ExternalAuth{Name: name, BackendUuid: policyBackendUuid, Path: "/check"}
			}

			It("should refuse a policy name defined twice", func() {
				virtServ := httpVsWithPolicies(newVirtualHost(backendUuid), policy("sso", backendUuid), policy("sso", backendUuid))

				_, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: virtServ})

				expectCode(err, connect.CodeInvalidArgument)
				Expect(err).To(MatchError(ContainSubstring("external auth sso is defined twice")))
			})
			It("should refuse a virtual host naming an undefined policy", func() {
				vhost := newVirtualHost(backendUuid)
				vhost.ExternalAuth = "missing"

				_, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: httpVsWithPolicies(vhost, policy("sso", backendUuid))})

				expectCode(err, connect.CodeInvalidArgument)
				Expect(err).To(MatchError(ContainSubstring("external auth missing is not defined")))
			})
			It("should refuse a route naming an undefined policy", func() {
				vhost := newVirtualHost(backendUuid)
				vhost.Routes[0].ExternalAuth = &route.Route_ExternalAuthName{ExternalAuthName: "missing"}

				_, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: httpVsWithPolicies(vhost, policy("sso", backendUuid))})

				expectCode(err, connect.CodeInvalidArgument)
				Expect(err).To(MatchError(ContainSubstring("external auth missing is not defined")))
			})
			It("should accept names that are defined", func() {
				vhost := newVirtualHost(backendUuid)
				vhost.ExternalAuth = "sso"
				vhost.Routes[0].ExternalAuth = &route.Route_ExternalAuthName{ExternalAuthName: "sso"}

				_, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: httpVsWithPolicies(vhost, policy("sso", backendUuid))})

				Expect(err).ToNot(HaveOccurred())
			})
			It("should refuse authorization as an upstream header", func() {
				sso := policy("sso", backendUuid)
				sso.UpstreamHeaders = []string{"x-user", "Authorization"}

				_, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: httpVsWithPolicies(newVirtualHost(backendUuid), sso)})

				expectCode(err, connect.CodeInvalidArgument)
				Expect(err).To(MatchError(ContainSubstring("external auth sso: upstream header authorization is also sent to the check")))
			})
			It("should refuse an upstream header also in the request headers", func() {
				sso := policy("sso", backendUuid)
				sso.RequestHeaders = []string{"cookie", "x-user"}
				sso.UpstreamHeaders = []string{"x-user"}

				_, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: httpVsWithPolicies(newVirtualHost(backendUuid), sso)})

				expectCode(err, connect.CodeInvalidArgument)
				Expect(err).To(MatchError(ContainSubstring("external auth sso: upstream header x-user is also sent to the check")))
			})
			It("should accept upstream headers the check doesn't read", func() {
				sso := policy("sso", backendUuid)
				sso.RequestHeaders = []string{"cookie"}
				sso.UpstreamHeaders = []string{"x-user"}

				_, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: httpVsWithPolicies(newVirtualHost(backendUuid), sso)})

				Expect(err).ToNot(HaveOccurred())
			})
			It("should check the policy backend exists", func() {
				virtServ := httpVsWithPolicies(newVirtualHost(backendUuid), policy("sso", "unknown"))

				_, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: virtServ})

				expectCode(err, connect.CodeNotFound)
			})
		})
		It("should refuse invalid virtual server", func() {
			client := svcvsconnect.NewVirtualServerClient(server.throughChain())
			_, err := client.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: newVs("vs-1", 0, backendUuid)})
			expectCode(err, connect.CodeInvalidArgument)
		})
	})

	Context("existing virtual server", func() {
		var vsUuid string
		BeforeEach(func() {
			resp, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: newVs("vs-1", 80, backendUuid)})
			Expect(err).ToNot(HaveOccurred())
			vsUuid = resp.GetVs().GetUUID()
		})

		It("should be readable by owner only", func() {
			resp, err := server.ReadVS(userCtx("bob"), &svccore.UuidRequest{UUID: vsUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetVs().GetPort()).To(Equal(uint32(80)))

			_, err = server.ReadVS(userCtx("alice"), &svccore.UuidRequest{UUID: vsUuid})
			expectCode(err, connect.CodePermissionDenied)
		})
		It("should update virtual server", func() {
			_, err := server.UpdateVS(userCtx("bob"), &svcvs.UpdateVSRequest{UUID: vsUuid, Vs: newVs("vs-1", 8080, backendUuid)})
			Expect(err).ToNot(HaveOccurred())

			resp, err := server.ReadVS(userCtx("bob"), &svccore.UuidRequest{UUID: vsUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetVs().GetPort()).To(Equal(uint32(8080)))
		})
		It("should refuse update with unknown backend", func() {
			_, err := server.UpdateVS(userCtx("bob"), &svcvs.UpdateVSRequest{UUID: vsUuid, Vs: newVs("vs-1", 80, "unknown")})
			expectCode(err, connect.CodeNotFound)
		})
		Context("on a vip", func() {
			passthroughVs := func(name, serverName string) *vs.VirtualServer {
				return &vs.VirtualServer{Name: name, Port: 443, RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{
					TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: []*vs.TlsTcpRouteConfig{{
						BackendUuid: backendUuid,
						Mode:        &vs.TlsTcpRouteConfig_Passthrough{Passthrough: &vs.TlsPassthrough{ServerNames: []string{serverName}}},
					}}},
				}}
			}
			createOnVip := func(virtServs ...*vs.VirtualServer) []string {
				uuids := make([]string, 0, 1+len(virtServs))
				uuids = append(uuids, vsUuid)
				for _, virtServ := range virtServs {
					resp, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: virtServ})
					Expect(err).ToNot(HaveOccurred())
					uuids = append(uuids, resp.GetVs().GetUUID())
				}
				_, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-1", uuids...)})
				Expect(err).ToNot(HaveOccurred())
				return uuids
			}

			It("should refuse an update putting a virtual server on the port of another of its vip", func() {
				uuids := createOnVip(newVs("vs-2", 81, backendUuid))

				_, err := server.UpdateVS(userCtx("bob"), &svcvs.UpdateVSRequest{UUID: uuids[1], Vs: newVs("vs-2", 80, backendUuid)})

				expectCode(err, connect.CodeInvalidArgument)
				Expect(err).To(MatchError(ContainSubstring("port tcp/80 is used by virtual servers")))
			})
			It("should accept an update of a virtual server alone on its port", func() {
				uuids := createOnVip(newVs("vs-2", 81, backendUuid))

				_, err := server.UpdateVS(userCtx("bob"), &svcvs.UpdateVSRequest{UUID: uuids[1], Vs: newVs("vs-2", 82, backendUuid)})

				Expect(err).ToNot(HaveOccurred())
			})
			It("should refuse an update conflicting on any vip of the virtual server", func() {
				uuids := createOnVip(newVs("vs-2", 81, backendUuid))
				vs3, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: newVs("vs-3", 82, backendUuid)})
				Expect(err).ToNot(HaveOccurred())
				vip2 := newVip("vip-2", uuids[1], vs3.GetVs().GetUUID())
				vip2.Ip = "10.0.0.2"
				_, err = server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: vip2})
				Expect(err).ToNot(HaveOccurred())

				_, err = server.UpdateVS(userCtx("bob"), &svcvs.UpdateVSRequest{UUID: uuids[1], Vs: newVs("vs-2", 82, backendUuid)})

				expectCode(err, connect.CodeInvalidArgument)
				Expect(err).To(MatchError(ContainSubstring("port tcp/82 is used by virtual servers vs-2, vs-3")))
			})
			It("should refuse turning on proxy protocol on one virtual server of a shared port", func() {
				uuids := createOnVip(passthroughVs("git", "git.local"), passthroughVs("db", "db.local"))
				withProxyProtocol := passthroughVs("db", "db.local")
				withProxyProtocol.GetTlsTcpRoutesConfig().AcceptProxyProtocol = true

				_, err := server.UpdateVS(userCtx("bob"), &svcvs.UpdateVSRequest{UUID: uuids[2], Vs: withProxyProtocol})

				expectCode(err, connect.CodeInvalidArgument)
				Expect(err).To(MatchError(ContainSubstring("accept_proxy_protocol")))
			})
		})
		It("should refuse update to non writer", func() {
			_, err := server.UpdateVS(userCtx("alice"), &svcvs.UpdateVSRequest{UUID: vsUuid, Vs: newVs("vs-1", 80, backendUuid)})
			expectCode(err, connect.CodePermissionDenied)
		})
		It("should list virtual servers of user", func() {
			resp, err := server.ListVS(userCtx("bob"), &svcvs.ListVSRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetVs()).To(HaveLen(1))
		})
		It("should delete virtual server", func() {
			_, err := server.DeleteVS(userCtx("bob"), &svccore.UuidRequest{UUID: vsUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(dbStoreMan.VirtualServers().Exists(context.Background(), vsUuid)).To(BeFalse())
		})
		It("should refuse to delete virtual server used by a vip", func() {
			_, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-1", vsUuid)})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.DeleteVS(userCtx("bob"), &svccore.UuidRequest{UUID: vsUuid})

			expectCode(err, connect.CodeFailedPrecondition)
		})
	})
})
