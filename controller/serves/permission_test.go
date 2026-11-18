package serves_test

import (
	"context"

	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/controller/lockers"
	"github.com/seagalia/flock/controller/permissioners"
	. "github.com/seagalia/flock/controller/serves"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	corev1 "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

// slowSpread holds the spread of a role to the members until release is closed
type slowSpread struct {
	permissioners.Permissioner
	release chan struct{}
}

func (p slowSpread) ListMemberPermissions(ctx context.Context, resType, resId string, role permission.Role) ([]*permission.MemberPermission, error) {
	<-p.release
	return p.Permissioner.ListMemberPermissions(ctx, resType, resId, role)
}

func user(name string) *permission.Member {
	return &permission.Member{MemberType: permission.MemberType_USER, MemberName: name}
}

var _ = Describe("Permission", func() {
	var server *testServer
	var backendUuid, vsUuid string
	backendType := helpers.TypeUrl[*backend.Backend]()
	vsType := helpers.TypeUrl[*vs.VirtualServer]()
	secretType := helpers.TypeUrl[*secret.Secret]()
	BeforeEach(func() {
		server = newTestServer(instantiaters.NewNull())
		backendResp, err := server.CreateBackend(userCtx("bob"), &svcbackend.CreateBackendRequest{Backend: newBackend("backend-1")})
		Expect(err).ToNot(HaveOccurred())
		backendUuid = backendResp.GetBackend().GetUUID()
		vsResp, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: newVs("vs-1", 80, backendUuid)})
		Expect(err).ToNot(HaveOccurred())
		vsUuid = vsResp.GetVs().GetUUID()
	})
	addPerm := func(resType, resId string, role permission.Role, member *permission.Member) {
		_, err := server.AddPermission(userCtx("bob"), &svcperm.AddPermissionRequest{
			ResourceType: resType, ResourceUuid: resId, Role: role, Member: member,
		})
		Expect(err).ToNot(HaveOccurred())
	}

	Context("AddPermission", func() {
		It("should give a role on a secret", func() {
			created, err := server.CreateSecret(userCtx("bob"), &svcsecret.CreateSecretRequest{
				Secret: &secret.Secret{Name: "secret-1", Data: map[string]string{"token": "s3cr3t"}},
			})
			Expect(err).ToNot(HaveOccurred())
			secretUuid := created.GetSecret().GetUUID()

			addPerm(secretType, secretUuid, permission.Role_WRITER, user("alice"))

			Expect(server.perm.HasPermission(context.Background(), secretType, secretUuid, permission.Role_WRITER, "alice", nil)).To(BeTrue())
		})
		It("should not give a role on the secret a backend points at", func() {
			created, err := server.CreateSecret(userCtx("bob"), &svcsecret.CreateSecretRequest{
				Secret: &secret.Secret{Name: "secret-1", Data: map[string]string{"token": "s3cr3t"}},
			})
			Expect(err).ToNot(HaveOccurred())
			secretUuid := created.GetSecret().GetUUID()
			pointing, err := dbStoreMan.Backends().Read(context.Background(), backendUuid)
			Expect(err).ToNot(HaveOccurred())
			pointing.Metadata = map[string]string{"auth": secret.RefPrefix + secretUuid + "/token"}
			Expect(dbStoreMan.Backends().Update(context.Background(), backendUuid, pointing)).To(Succeed())

			addPerm(backendType, backendUuid, permission.Role_OWNER, user("alice"))

			Expect(server.perm.HasPermission(context.Background(), backendType, backendUuid, permission.Role_OWNER, "alice", nil)).To(BeTrue())
			Expect(server.perm.HasPermission(context.Background(), secretType, secretUuid, permission.Role_READER, "alice", nil)).To(BeFalse())
		})
		It("should let owner give permission to another user", func() {
			addPerm(backendType, backendUuid, permission.Role_READER, user("alice"))

			_, err := server.ReadBackend(userCtx("alice"), &svccore.UuidRequest{UUID: backendUuid})
			Expect(err).ToNot(HaveOccurred())
		})
		It("should give same permission on resources used by the resource", func() {
			addPerm(vsType, vsUuid, permission.Role_READER, user("alice"))

			Expect(server.perm.HasPermission(context.Background(), vsType, vsUuid, permission.Role_READER, "alice", nil)).To(BeTrue())
			Expect(server.perm.HasPermission(context.Background(), backendType, backendUuid, permission.Role_READER, "alice", nil)).To(BeTrue())
		})
		It("should be refused to non owner", func() {
			addPerm(backendType, backendUuid, permission.Role_WRITER, user("alice"))

			_, err := server.AddPermission(userCtx("alice"), &svcperm.AddPermissionRequest{
				ResourceType: backendType, ResourceUuid: backendUuid, Role: permission.Role_READER, Member: user("eve"),
			})

			expectCode(err, connect.CodePermissionDenied)
		})
		It("should ignore permissions on reserved resources", func() {
			reservedUuid := ReservedUuidPrefix + "000000000001"
			reserved := newBackend("default")
			reserved.UUID = reservedUuid
			Expect(dbStoreMan.Backends().Create(context.Background(), reserved)).To(Succeed())

			_, err := server.AddPermission(userCtx(adminUser), &svcperm.AddPermissionRequest{
				ResourceType: backendType, ResourceUuid: reservedUuid, Role: permission.Role_OWNER, Member: user("alice"),
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(server.perm.ListMemberPermissions(context.Background(), backendType, reservedUuid, -1)).To(BeEmpty())
		})
	})

	Context("DeletePermission", func() {
		It("should remove permission of member and on resources used by the resource", func() {
			addPerm(vsType, vsUuid, permission.Role_READER, user("alice"))

			_, err := server.DeletePermission(userCtx("bob"), &svcperm.DeletePermissionRequest{
				ResourceType: vsType, ResourceUuid: vsUuid, Member: user("alice"),
			})
			Expect(err).ToNot(HaveOccurred())

			Expect(server.perm.HasPermission(context.Background(), vsType, vsUuid, permission.Role_READER, "alice", nil)).To(BeFalse())
			Expect(server.perm.HasPermission(context.Background(), backendType, backendUuid, permission.Role_READER, "alice", nil)).To(BeFalse())
		})
		It("should refuse to delete last owner", func() {
			_, err := server.DeletePermission(userCtx("bob"), &svcperm.DeletePermissionRequest{
				ResourceType: backendType, ResourceUuid: backendUuid, Member: user("bob"),
			})
			expectCode(err, connect.CodeFailedPrecondition)
		})
		It("should be refused to non owner", func() {
			_, err := server.DeletePermission(userCtx("alice"), &svcperm.DeletePermissionRequest{
				ResourceType: backendType, ResourceUuid: backendUuid, Member: user("bob"),
			})
			expectCode(err, connect.CodePermissionDenied)
		})
	})

	Context("HasPermission", func() {
		It("should tell if member has permission", func() {
			addPerm(backendType, backendUuid, permission.Role_WRITER, &permission.Member{
				MemberType: permission.MemberType_GROUP, MemberName: "team",
			})

			resp, err := server.HasPermission(userCtx("bob"), &svcperm.HasPermissionRequest{
				ResourceType: backendType, ResourceUuid: backendUuid, Role: permission.Role_WRITER,
				Member: &permission.Member{MemberType: permission.MemberType_GROUP, MemberName: "team"},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetHasPermission()).To(BeTrue())

			resp, err = server.HasPermission(userCtx("bob"), &svcperm.HasPermissionRequest{
				ResourceType: backendType, ResourceUuid: backendUuid, Role: permission.Role_WRITER, Member: user("alice"),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetHasPermission()).To(BeFalse())
		})
		It("should be refused to user who cannot read the resource", func() {
			_, err := server.HasPermission(userCtx("alice"), &svcperm.HasPermissionRequest{
				ResourceType: backendType, ResourceUuid: backendUuid, Role: permission.Role_READER, Member: user("alice"),
			})
			expectCode(err, connect.CodePermissionDenied)
		})
	})

	Context("ListMemberPermissions", func() {
		BeforeEach(func() {
			addPerm(backendType, backendUuid, permission.Role_READER, user("alice"))
		})
		It("should list members of a role", func() {
			resp, err := server.ListMemberPermissions(userCtx("bob"), &svcperm.ListMemberPermissionsRequest{
				ResourceType: backendType, ResourceUuid: backendUuid, Role: permission.Role_OWNER,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetMemberPermissions()).To(HaveLen(1))
			Expect(resp.GetMemberPermissions()[0].GetMember().GetMemberName()).To(Equal("bob"))
		})
		It("should list members of all roles", func() {
			resp, err := server.ListMemberPermissions(userCtx("bob"), &svcperm.ListMemberPermissionsRequest{
				ResourceType: backendType, ResourceUuid: backendUuid, SeeAll: true,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetMemberPermissions()).To(HaveLen(2))
		})
		It("should tell whether everyone can read the resource", func() {
			listReq := &svcperm.ListMemberPermissionsRequest{ResourceType: backendType, ResourceUuid: backendUuid}

			resp, err := server.ListMemberPermissions(userCtx("bob"), listReq)
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetAllowAllRead()).To(BeFalse())

			Expect(server.perm.SetAllowAllRead(context.Background(), backendType, backendUuid, true)).To(Succeed())

			resp, err = server.ListMemberPermissions(userCtx("bob"), listReq)
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetAllowAllRead()).To(BeTrue())
		})
	})

	Context("SetAllowAllRead", func() {
		It("should let anyone read the resource", func() {
			_, err := server.SetAllowAllRead(userCtx("bob"), &svcperm.SetAllowAllReadRequest{
				ResourceType: backendType, ResourceUuid: backendUuid, AllowAllRead: true,
			})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.ReadBackend(userCtx("anyone"), &svccore.UuidRequest{UUID: backendUuid})
			Expect(err).ToNot(HaveOccurred())
		})
		It("should be refused to non owner", func() {
			_, err := server.SetAllowAllRead(userCtx("alice"), &svcperm.SetAllowAllReadRequest{
				ResourceType: backendType, ResourceUuid: backendUuid, AllowAllRead: true,
			})
			expectCode(err, connect.CodePermissionDenied)
		})
	})

	Context("Permission spreading on update", func() {
		It("should keep role of members when resource is updated", func() {
			addPerm(backendType, backendUuid, permission.Role_READER, user("alice"))

			_, err := server.UpdateBackend(userCtx("bob"), &svcbackend.UpdateBackendRequest{UUID: backendUuid, Backend: newBackend("backend-1")})
			Expect(err).ToNot(HaveOccurred())

			server.WaitBackground()
			Expect(server.perm.HasPermission(context.Background(), backendType, backendUuid, permission.Role_OWNER, "alice", nil)).To(BeFalse())
			Expect(server.perm.HasPermission(context.Background(), backendType, backendUuid, permission.Role_READER, "alice", nil)).To(BeTrue())
		})
		It("should give members permission on resources newly used by the resource", func() {
			addPerm(vsType, vsUuid, permission.Role_READER, user("alice"))
			otherBackend, err := server.CreateBackend(userCtx("bob"), &svcbackend.CreateBackendRequest{Backend: newBackend("backend-2")})
			Expect(err).ToNot(HaveOccurred())
			otherBackendUuid := otherBackend.GetBackend().GetUUID()

			_, err = server.UpdateVS(userCtx("bob"), &svcvs.UpdateVSRequest{UUID: vsUuid, Vs: newVs("vs-1", 80, otherBackendUuid)})
			Expect(err).ToNot(HaveOccurred())

			server.WaitBackground()
			Expect(server.perm.HasPermission(context.Background(), backendType, otherBackendUuid, permission.Role_READER, "alice", nil)).To(BeTrue())
		})
		It("should answer before the spread ends and wait for it on WaitBackground", func() {
			addPerm(backendType, backendUuid, permission.Role_READER, user("alice"))
			release := make(chan struct{})
			admConf := config.AdminConfig{Users: []string{adminUser}, Groups: []string{adminGroup}}
			slow := NewFlockServer(context.Background(), nil, admConf, nil, dbStoreMan, nil, nil,
				slowSpread{Permissioner: server.perm, release: release}, instantiaters.NewNull(), nil, lockers.NewMemory(), nil)
			DeferCleanup(func() {
				close(release)
				slow.WaitBackground()
			})

			answered := make(chan error, 1)
			go func() {
				_, err := slow.UpdateBackend(userCtx("bob"), &svcbackend.UpdateBackendRequest{UUID: backendUuid, Backend: newBackend("backend-1")})
				answered <- err
			}()
			Eventually(answered, time.Second).Should(Receive(BeNil()))

			waited := make(chan struct{})
			go func() {
				slow.WaitBackground()
				close(waited)
			}()
			Consistently(waited, 100*time.Millisecond).ShouldNot(BeClosed())
		})
	})

	Context("Referenced resources", func() {
		expectNoRoleSpread := func(resType, resId, username string) {
			GinkgoHelper()
			server.WaitBackground()
			perms, err := server.perm.ListMemberPermissions(context.Background(), resType, resId, -1)
			Expect(err).ToNot(HaveOccurred())
			Expect(lo.Filter(perms, func(perm *permission.MemberPermission, _ int) bool {
				return perm.GetMember().GetMemberName() == username
			})).To(BeEmpty())
		}

		It("should refuse a virtual server referencing a backend the user cannot read", func() {
			_, err := server.CreateVS(userCtx("alice"), &svcvs.CreateVSRequest{Vs: newVs("vs-alice", 81, backendUuid)})

			expectCode(err, connect.CodePermissionDenied)
		})
		It("should refuse to update a virtual server to reference a backend the user cannot read", func() {
			aliceBackend, err := server.CreateBackend(userCtx("alice"), &svcbackend.CreateBackendRequest{Backend: newBackend("backend-alice")})
			Expect(err).ToNot(HaveOccurred())
			aliceVs, err := server.CreateVS(userCtx("alice"), &svcvs.CreateVSRequest{
				Vs: newVs("vs-alice", 81, aliceBackend.GetBackend().GetUUID()),
			})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.UpdateVS(userCtx("alice"), &svcvs.UpdateVSRequest{
				UUID: aliceVs.GetVs().GetUUID(), Vs: newVs("vs-alice", 81, backendUuid),
			})

			expectCode(err, connect.CodePermissionDenied)
			expectNoRoleSpread(backendType, backendUuid, "alice")
		})
		It("should refuse a virtual server referencing a certificate the user cannot read", func() {
			certPem, keyPem := generateCertPair()
			certResp, err := server.CreateCertificate(userCtx("bob"), &svccert.CreateCertificateRequest{Certificate: &cert.Certificate{
				Name: "cert-1", CertChain: certPem, PrivateKey: keyPem, Ca: certPem,
			}})
			Expect(err).ToNot(HaveOccurred())
			Expect(server.perm.SetAllowAllRead(context.Background(), backendType, backendUuid, true)).To(Succeed())
			virtHost := newVirtualHost(backendUuid)
			virtHost.TlsOptions = []*corev1.TLS{{ServerNames: []string{"test.local"}, CertificatesUuid: []string{certResp.GetCertificate().GetUUID()}}}

			_, err = server.CreateVS(userCtx("alice"), &svcvs.CreateVSRequest{Vs: newHttpVs("vs-alice", 443, virtHost)})

			expectCode(err, connect.CodePermissionDenied)
		})
		It("should let a user reference a backend readable by all without getting a role on it", func() {
			Expect(server.perm.SetAllowAllRead(context.Background(), backendType, backendUuid, true)).To(Succeed())

			_, err := server.CreateVS(userCtx("alice"), &svcvs.CreateVSRequest{Vs: newVs("vs-alice", 81, backendUuid)})
			Expect(err).ToNot(HaveOccurred())

			expectNoRoleSpread(backendType, backendUuid, "alice")
			_, err = server.ReadBackend(userCtx("alice"), &svccore.UuidRequest{UUID: backendUuid})
			Expect(err).ToNot(HaveOccurred())
		})
		It("should let a reader reference a backend without becoming its owner", func() {
			Expect(server.perm.AddPermission(context.Background(), backendType, backendUuid, permission.Role_READER, permission.MemberType_USER, "alice")).To(Succeed())

			_, err := server.CreateVS(userCtx("alice"), &svcvs.CreateVSRequest{Vs: newVs("vs-alice", 81, backendUuid)})
			Expect(err).ToNot(HaveOccurred())

			Consistently(func() (bool, error) {
				return server.perm.HasPermission(context.Background(), backendType, backendUuid, permission.Role_WRITER, "alice", nil)
			}).WithTimeout(300 * time.Millisecond).Should(BeFalse())
		})
		It("should not spread a role given on a virtual server to a backend its owner only reads", func() {
			Expect(server.perm.AddPermission(context.Background(), backendType, backendUuid, permission.Role_READER, permission.MemberType_USER, "alice")).To(Succeed())
			aliceVs, err := server.CreateVS(userCtx("alice"), &svcvs.CreateVSRequest{Vs: newVs("vs-alice", 81, backendUuid)})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.AddPermission(userCtx("alice"), &svcperm.AddPermissionRequest{
				ResourceType: vsType, ResourceUuid: aliceVs.GetVs().GetUUID(), Role: permission.Role_OWNER, Member: user("eve"),
			})
			Expect(err).ToNot(HaveOccurred())

			expectNoRoleSpread(backendType, backendUuid, "eve")
		})
		It("should refuse a vip listing a virtual server the user cannot read", func() {
			vipResp, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-1")})
			Expect(err).ToNot(HaveOccurred())
			vipUuid := vipResp.GetVip().GetUUID()
			Expect(server.perm.AddPermission(context.Background(), helpers.TypeUrl[*vip.VIP](), vipUuid, permission.Role_WRITER, permission.MemberType_USER, "alice")).To(Succeed())

			_, err = server.UpdateVIP(userCtx("alice"), &svcvip.UpdateVIPRequest{UUID: vipUuid, Vip: newVip("vip-1", vsUuid)})

			expectCode(err, connect.CodePermissionDenied)
			expectNoRoleSpread(vsType, vsUuid, "alice")
		})
		It("should not make the user owner of a vip it adds to its instance", func() {
			zoneUuid := server.createZone("zone-1")
			vipResp, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-1")})
			Expect(err).ToNot(HaveOccurred())
			vipUuid := vipResp.GetVip().GetUUID()
			Expect(server.perm.AddPermission(context.Background(), helpers.TypeUrl[*vip.VIP](), vipUuid, permission.Role_READER, permission.MemberType_USER, "alice")).To(Succeed())
			instResp, err := server.CreateInstance(userCtx("alice"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid)})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.AddVipToInstance(userCtx("alice"), &svcinstance.AddVipToInstanceRequest{UUID: instResp.GetInstance().GetUUID(), VipUuid: vipUuid})
			Expect(err).ToNot(HaveOccurred())

			Consistently(func() (bool, error) {
				return server.perm.HasPermission(context.Background(), helpers.TypeUrl[*vip.VIP](), vipUuid, permission.Role_WRITER, "alice", nil)
			}).WithTimeout(300 * time.Millisecond).Should(BeFalse())
		})
	})
})
