package serves_test

import (
	"context"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/controller/lockers"
	"github.com/seagalia/flock/controller/permissioners"
	. "github.com/seagalia/flock/controller/serves"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

// cancelOnGrant cancels the request when the owner is granted, as a client leaving right after the backend is stored
type cancelOnGrant struct {
	permissioners.Permissioner
	cancel context.CancelFunc
}

func (c cancelOnGrant) AddPermission(
	ctx context.Context, resType, resId string, role permission.Role, memberType permission.MemberType, member string,
) error {
	c.cancel()
	return c.Permissioner.AddPermission(ctx, resType, resId, role, memberType, member)
}

var _ = Describe("Backend", func() {
	var server *testServer
	backendType := helpers.TypeUrl[*backend.Backend]()
	BeforeEach(func() {
		server = newTestServer(instantiaters.NewNull())
	})

	Context("CreateBackend", func() {
		It("should create backend owned by user", func() {
			resp, err := server.CreateBackend(userCtx("bob"), &svcbackend.CreateBackendRequest{Backend: newBackend("backend-1")})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetBackend().GetUUID()).ToNot(BeEmpty())

			Expect(server.perm.HasPermission(context.Background(), backendType, resp.GetBackend().GetUUID(), permission.Role_OWNER, "bob", nil)).To(BeTrue())
		})
		It("should give the backend its owner even when the request is cancelled once it is stored", func() {
			perm := permissioners.NewDbStorePerm(dbStoreMan)
			ctx, cancel := context.WithCancel(userCtx("bob"))
			DeferCleanup(cancel)
			admConf := config.AdminConfig{Users: []string{adminUser}, Groups: []string{adminGroup}}
			cancelling := NewFlockServer(context.Background(), nil, admConf, nil, dbStoreMan, nil, nil,
				cancelOnGrant{Permissioner: perm, cancel: cancel}, instantiaters.NewNull(), nil, lockers.NewMemory(), nil)
			DeferCleanup(cancelling.WaitBackground)

			resp, err := cancelling.CreateBackend(ctx, &svcbackend.CreateBackendRequest{Backend: newBackend("backend-1")})

			Expect(err).ToNot(HaveOccurred())
			Expect(perm.HasPermission(context.Background(), backendType, resp.GetBackend().GetUUID(), permission.Role_OWNER, "bob", nil)).To(BeTrue())
		})
		It("should not give ownership to admin", func() {
			resp, err := server.CreateBackend(userCtx(adminUser), &svcbackend.CreateBackendRequest{Backend: newBackend("backend-1")})
			Expect(err).ToNot(HaveOccurred())

			Expect(server.perm.ListMemberPermissions(context.Background(), backendType, resp.GetBackend().GetUUID(), -1)).To(BeEmpty())
		})
		It("should refuse invalid backend", func() {
			client := svcbackendconnect.NewBackendClient(server.throughChain())
			_, err := client.CreateBackend(userCtx("bob"), &svcbackend.CreateBackendRequest{Backend: &backend.Backend{}})
			expectCode(err, connect.CodeInvalidArgument)
		})
		It("should give its defaults to a backend before validating it", func() {
			// the rules require thresholds above zero, the backend has none
			withoutThresholds := newBackend("backend-defaults")
			client := svcbackendconnect.NewBackendClient(server.throughChain())

			resp, err := client.CreateBackend(userCtx("bob"), &svcbackend.CreateBackendRequest{Backend: withoutThresholds})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetBackend().GetHealthCheck().GetUnhealthyThreshold()).To(BeEquivalentTo(3))
			Expect(resp.GetBackend().GetHealthCheck().GetHealthyThreshold()).To(BeEquivalentTo(1))
		})
	})

	Context("existing backend", func() {
		var backendUuid string
		BeforeEach(func() {
			resp, err := server.CreateBackend(userCtx("bob"), &svcbackend.CreateBackendRequest{Backend: newBackend("backend-1")})
			Expect(err).ToNot(HaveOccurred())
			backendUuid = resp.GetBackend().GetUUID()
		})

		Context("ReadBackend", func() {
			It("should answer Canceled when the request of a user is cancelled during the permission check", func() {
				ctx, cancel := context.WithCancel(userCtx("bob"))
				cancel()

				_, err := server.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})

				expectCode(err, connect.CodeCanceled)
			})
			It("should answer Canceled when the request is cancelled", func() {
				ctx, cancel := context.WithCancel(userCtx(adminUser))
				cancel()

				_, err := server.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})

				expectCode(err, connect.CodeCanceled)
			})
			It("should be readable by owner and admin", func() {
				resp, err := server.ReadBackend(userCtx("bob"), &svccore.UuidRequest{UUID: backendUuid})
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetBackend().GetName()).To(Equal("backend-1"))

				_, err = server.ReadBackend(userCtx("someone", adminGroup), &svccore.UuidRequest{UUID: backendUuid})
				Expect(err).ToNot(HaveOccurred())
			})
			It("should be refused to other users", func() {
				_, err := server.ReadBackend(userCtx("alice"), &svccore.UuidRequest{UUID: backendUuid})
				expectCode(err, connect.CodePermissionDenied)
			})
			It("should be readable by group member given reader permission", func() {
				Expect(server.perm.AddPermission(context.Background(), backendType, backendUuid, permission.Role_READER, permission.MemberType_GROUP, "team")).To(Succeed())

				_, err := server.ReadBackend(userCtx("alice", "team"), &svccore.UuidRequest{UUID: backendUuid})
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("UpdateBackend", func() {
			It("should update backend as owner", func() {
				updated := newBackend("backend-renamed")

				_, err := server.UpdateBackend(userCtx("bob"), &svcbackend.UpdateBackendRequest{UUID: backendUuid, Backend: updated})
				Expect(err).ToNot(HaveOccurred())

				resp, err := server.ReadBackend(userCtx("bob"), &svccore.UuidRequest{UUID: backendUuid})
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetBackend().GetName()).To(Equal("backend-renamed"))
			})
			It("should allow writer to update without renaming", func() {
				Expect(server.perm.AddPermission(context.Background(), backendType, backendUuid, permission.Role_WRITER, permission.MemberType_USER, "alice")).To(Succeed())

				_, err := server.UpdateBackend(userCtx("alice"), &svcbackend.UpdateBackendRequest{UUID: backendUuid, Backend: newBackend("backend-1")})
				Expect(err).ToNot(HaveOccurred())
			})
			It("should refuse writer to rename", func() {
				Expect(server.perm.AddPermission(context.Background(), backendType, backendUuid, permission.Role_WRITER, permission.MemberType_USER, "alice")).To(Succeed())

				_, err := server.UpdateBackend(userCtx("alice"), &svcbackend.UpdateBackendRequest{UUID: backendUuid, Backend: newBackend("backend-renamed")})
				expectCode(err, connect.CodePermissionDenied)
			})
			It("should refuse reader to update", func() {
				Expect(server.perm.AddPermission(context.Background(), backendType, backendUuid, permission.Role_READER, permission.MemberType_USER, "alice")).To(Succeed())

				_, err := server.UpdateBackend(userCtx("alice"), &svcbackend.UpdateBackendRequest{UUID: backendUuid, Backend: newBackend("backend-1")})
				expectCode(err, connect.CodePermissionDenied)
			})
		})

		Context("DeleteBackend", func() {
			It("should delete backend as owner", func() {
				_, err := server.DeleteBackend(userCtx("bob"), &svccore.UuidRequest{UUID: backendUuid})
				Expect(err).ToNot(HaveOccurred())

				Expect(dbStoreMan.Backends().Exists(context.Background(), backendUuid)).To(BeFalse())
			})
			It("should refuse writer to delete", func() {
				Expect(server.perm.AddPermission(context.Background(), backendType, backendUuid, permission.Role_WRITER, permission.MemberType_USER, "alice")).To(Succeed())

				_, err := server.DeleteBackend(userCtx("alice"), &svccore.UuidRequest{UUID: backendUuid})
				expectCode(err, connect.CodePermissionDenied)
			})
			It("should refuse to delete backend used by a virtual server", func() {
				_, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: newVs("vs-1", 80, backendUuid)})
				Expect(err).ToNot(HaveOccurred())

				_, err = server.DeleteBackend(userCtx("bob"), &svccore.UuidRequest{UUID: backendUuid})
				expectCode(err, connect.CodeFailedPrecondition)
			})
		})

		Context("ListBackend", func() {
			BeforeEach(func() {
				_, err := server.CreateBackend(userCtx("alice"), &svcbackend.CreateBackendRequest{Backend: newBackend("backend-2")})
				Expect(err).ToNot(HaveOccurred())
			})
			It("should only list backends of user", func() {
				resp, err := server.ListBackend(userCtx("bob"), &svcbackend.ListBackendRequest{})
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetBackends()).To(HaveLen(1))
				Expect(resp.GetBackends()[0].GetName()).To(Equal("backend-1"))
			})
			It("should list all backends for admin", func() {
				resp, err := server.ListBackend(userCtx(adminUser), &svcbackend.ListBackendRequest{})
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetBackends()).To(HaveLen(2))
			})
			It("should list backends of the user admin act as", func() {
				ctx := withAsUser(userCtx(adminUser), `{"username": "alice", "groups": []}`)

				resp, err := server.ListBackend(ctx, &svcbackend.ListBackendRequest{})

				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetBackends()).To(HaveLen(1))
				Expect(resp.GetBackends()[0].GetName()).To(Equal("backend-2"))
			})
			It("should ignore invalid acting user", func() {
				ctx := withAsUser(userCtx(adminUser), `not json`)

				resp, err := server.ListBackend(ctx, &svcbackend.ListBackendRequest{})

				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetBackends()).To(HaveLen(2))
			})
			It("should not let non admin act as another user", func() {
				ctx := withAsUser(userCtx("bob"), `{"username": "alice", "groups": []}`)

				resp, err := server.ListBackend(ctx, &svcbackend.ListBackendRequest{})

				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetBackends()).To(HaveLen(1))
				Expect(resp.GetBackends()[0].GetName()).To(Equal("backend-1"))
			})
		})
	})
})
