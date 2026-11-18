//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"

	"github.com/seagalia/flock/e2e/controller/env"
)

// Postgres because the sql store is the only one wiring the permissioner, the users call with ID tokens of the
// provider so their groups come from the token
var _ = Describe("Permission", Ordered, func() {
	var (
		g                       *env.Controller
		alice, bob, frank, root *env.Api
	)

	BeforeAll(func(ctx SpecContext) {
		dsn, err := e2eEnv.StartPostgres(ctx)
		Expect(err).NotTo(HaveOccurred())
		provider, err := e2eEnv.StartOauth2(ctx)
		Expect(err).NotTo(HaveOccurred())
		g = startController(ctx, env.ControllerOptions{Name: "controller-permission", Store: env.StorePostgres, DSN: dsn, Oidc: provider})
		alice = loginOidc(ctx, g, provider, "alice")
		bob = loginOidc(ctx, g, provider, "bob")
		frank = loginOidc(ctx, g, provider, "frank")
		root = loginOidc(ctx, g, provider, "root")
	}, NodeTimeout(10*time.Minute))

	BeforeEach(func() {
		registerControllerForDump(g)
	})

	It("keeps resources private to their owner", func(ctx SpecContext) {
		vsUuid, backendUuid := createTcpVs(ctx, alice, 8080)

		_, err := bob.VS.ReadVS(ctx, &svccore.UuidRequest{UUID: vsUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)
		_, err = bob.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)

		Expect(listedVsUuids(ctx, bob)).NotTo(ContainElement(vsUuid))
		backends, err := bob.Backend.ListBackend(ctx, &svcbackend.ListBackendRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(backends.GetBackends()).NotTo(ContainElement(HaveField("UUID", backendUuid)))
	}, NodeTimeout(time.Minute))

	It("spreads a reader grant to used resources", func(ctx SpecContext) {
		vsUuid, backendUuid := createTcpVs(ctx, alice, 8080)

		grant(ctx, alice, vsType, vsUuid, permission.Role_READER, user("bob"))

		Eventually(func() error {
			_, err := bob.VS.ReadVS(ctx, &svccore.UuidRequest{UUID: vsUuid})
			return err
		}).Should(Succeed())
		Eventually(func() error {
			_, err := bob.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
			return err
		}).Should(Succeed())

		vsResp, err := alice.VS.ReadVS(ctx, &svccore.UuidRequest{UUID: vsUuid})
		Expect(err).NotTo(HaveOccurred())
		_, err = bob.VS.UpdateVS(ctx, &svcvs.UpdateVSRequest{UUID: vsUuid, Vs: vsResp.GetVs()})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)
		_, err = bob.VS.DeleteVS(ctx, &svccore.UuidRequest{UUID: vsUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)
		_, err = bob.Backend.DeleteBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)
	}, NodeTimeout(time.Minute))

	// regression: the spreading of an update gave the owner role to every member of the resource
	It("keeps a reader a reader after the owner updates", func(ctx SpecContext) {
		vsUuid, backendUuid := createTcpVs(ctx, alice, 8080)
		grant(ctx, alice, vsType, vsUuid, permission.Role_READER, user("bob"))
		newBackendResp, err := alice.Backend.CreateBackend(ctx, &svcbackend.CreateBackendRequest{
			Backend: env.NewBackend(env.Unique("backend")),
		})
		Expect(err).NotTo(HaveOccurred())
		newBackendUuid := newBackendResp.GetBackend().GetUUID()

		// moving the vs to a new backend lets the spec see the background spreading has run
		vsResp, err := alice.VS.ReadVS(ctx, &svccore.UuidRequest{UUID: vsUuid})
		Expect(err).NotTo(HaveOccurred())
		updated := env.NewTcpVs(vsResp.GetVs().GetName(), 8080, newBackendUuid)
		_, err = alice.VS.UpdateVS(ctx, &svcvs.UpdateVSRequest{UUID: vsUuid, Vs: updated})
		Expect(err).NotTo(HaveOccurred())
		Eventually(hasRole(ctx, alice, backendType, newBackendUuid, permission.Role_READER, user("bob"))).Should(BeTrue())

		// WRITER also covers OWNER, a role is held by whoever has it or a higher one
		bobPromoted := func() bool {
			onVs := hasRole(ctx, alice, vsType, vsUuid, permission.Role_WRITER, user("bob"))()
			onBackend := hasRole(ctx, alice, backendType, backendUuid, permission.Role_WRITER, user("bob"))()
			onNewBackend := hasRole(ctx, alice, backendType, newBackendUuid, permission.Role_WRITER, user("bob"))()
			return onVs || onBackend || onNewBackend
		}
		Consistently(bobPromoted).WithTimeout(2 * time.Second).Should(BeFalse())
		Expect(hasRole(ctx, alice, vsType, vsUuid, permission.Role_READER, user("bob"))()).To(BeTrue())
		Expect(hasRole(ctx, alice, vsType, vsUuid, permission.Role_OWNER, user("alice"))()).To(BeTrue())
	}, NodeTimeout(time.Minute))

	It("grants through groups from the token", func(ctx SpecContext) {
		backendUuid := createBackend(ctx, alice)
		_, err := frank.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)

		grant(ctx, alice, backendType, backendUuid, permission.Role_READER, group("team-x"))

		Eventually(func() error {
			_, err := frank.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
			return err
		}).Should(Succeed())
		backends, err := frank.Backend.ListBackend(ctx, &svcbackend.ListBackendRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(backends.GetBackends()).To(ContainElement(HaveField("UUID", backendUuid)))
	}, NodeTimeout(time.Minute))

	It("lets anyone read when allow all read is set", func(ctx SpecContext) {
		backendUuid := createBackend(ctx, alice)
		_, err := bob.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)

		_, err = alice.Perm.SetAllowAllRead(ctx, &svcperm.SetAllowAllReadRequest{
			ResourceType: backendType, ResourceUuid: backendUuid, AllowAllRead: true,
		})
		Expect(err).NotTo(HaveOccurred())

		_, err = bob.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
		Expect(err).NotTo(HaveOccurred())
		_, err = frank.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
		Expect(err).NotTo(HaveOccurred())
		_, err = bob.Backend.DeleteBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)
	}, NodeTimeout(time.Minute))

	It("refuses a reference to a backend the user cannot read", func(ctx SpecContext) {
		backendUuid := createBackend(ctx, alice)

		_, err := bob.VS.CreateVS(ctx, &svcvs.CreateVSRequest{Vs: env.NewTcpVs(env.Unique("vs"), 8080, backendUuid)})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)

		bobVsUuid, _ := createTcpVs(ctx, bob, 8080)
		bobVs, err := bob.VS.ReadVS(ctx, &svccore.UuidRequest{UUID: bobVsUuid})
		Expect(err).NotTo(HaveOccurred())
		updated := env.NewTcpVs(bobVs.GetVs().GetName(), 8080, backendUuid)
		_, err = bob.VS.UpdateVS(ctx, &svcvs.UpdateVSRequest{UUID: bobVsUuid, Vs: updated})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)

		Consistently(hasRole(ctx, alice, backendType, backendUuid, permission.Role_READER, user("bob"))).
			WithTimeout(2 * time.Second).Should(BeFalse())
	}, NodeTimeout(time.Minute))

	It("lets a user reference a backend readable by all without a role on it", func(ctx SpecContext) {
		backendUuid := createBackend(ctx, alice)
		_, err := alice.Perm.SetAllowAllRead(ctx, &svcperm.SetAllowAllReadRequest{
			ResourceType: backendType, ResourceUuid: backendUuid, AllowAllRead: true,
		})
		Expect(err).NotTo(HaveOccurred())

		_, err = bob.VS.CreateVS(ctx, &svcvs.CreateVSRequest{Vs: env.NewTcpVs(env.Unique("vs"), 8080, backendUuid)})
		Expect(err).NotTo(HaveOccurred())

		// READER would be true through allow all read, WRITER also covers OWNER
		Consistently(hasRole(ctx, alice, backendType, backendUuid, permission.Role_WRITER, user("bob"))).
			WithTimeout(2 * time.Second).Should(BeFalse())
		_, err = bob.Backend.DeleteBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)
	}, NodeTimeout(time.Minute))

	It("lets the admin group see everything and act as a user", func(ctx SpecContext) {
		sharedVsUuid, _ := createTcpVs(ctx, alice, 8080)
		privateVsUuid, _ := createTcpVs(ctx, alice, 8080)
		grant(ctx, alice, vsType, sharedVsUuid, permission.Role_READER, user("bob"))

		Expect(listedVsUuids(ctx, root)).To(ContainElements(sharedVsUuid, privateVsUuid))
		_, err := root.VS.ReadVS(ctx, &svccore.UuidRequest{UUID: privateVsUuid})
		Expect(err).NotTo(HaveOccurred())

		asBob, err := json.Marshal(map[string]any{"username": "bob", "groups": env.Oauth2Users["bob"]})
		Expect(err).NotTo(HaveOccurred())
		rootAsBobCtx, callInfo := connect.NewClientContext(ctx)
		callInfo.RequestHeader().Set("as_user", string(asBob))
		bobVsUuids := listedVsUuids(ctx, bob)
		Expect(bobVsUuids).To(ContainElement(sharedVsUuid))
		Expect(bobVsUuids).NotTo(ContainElement(privateVsUuid))
		Expect(listedVsUuids(rootAsBobCtx, root)).To(ConsistOf(bobVsUuids))
	}, NodeTimeout(time.Minute))

	It("refuses to remove the last owner", func(ctx SpecContext) {
		backendUuid := createBackend(ctx, alice)

		_, err := alice.Perm.DeletePermission(ctx, &svcperm.DeletePermissionRequest{
			ResourceType: backendType, ResourceUuid: backendUuid, Member: user("alice"),
		})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition), "%v", err)
		Expect(hasRole(ctx, alice, backendType, backendUuid, permission.Role_OWNER, user("alice"))()).To(BeTrue())
	}, NodeTimeout(time.Minute))
})

func user(name string) *permission.Member {
	return &permission.Member{MemberType: permission.MemberType_USER, MemberName: name}
}

func group(name string) *permission.Member {
	return &permission.Member{MemberType: permission.MemberType_GROUP, MemberName: name}
}

func createBackend(ctx context.Context, owner *env.Api) string {
	GinkgoHelper()
	resp, err := owner.Backend.CreateBackend(ctx, &svcbackend.CreateBackendRequest{
		Backend: env.NewBackend(env.Unique("backend")),
	})
	Expect(err).NotTo(HaveOccurred())
	return resp.GetBackend().GetUUID()
}

func grant(ctx context.Context, owner *env.Api, resType, resUuid string, role permission.Role, member *permission.Member) {
	GinkgoHelper()
	_, err := owner.Perm.AddPermission(ctx, &svcperm.AddPermissionRequest{
		ResourceType: resType, ResourceUuid: resUuid, Role: role, Member: member,
	})
	Expect(err).NotTo(HaveOccurred())
}

// hasRole is polled by Eventually and Consistently
func hasRole(ctx context.Context, observer *env.Api, resType, resUuid string, role permission.Role, member *permission.Member) func() bool {
	return func() bool {
		GinkgoHelper()
		resp, err := observer.Perm.HasPermission(ctx, &svcperm.HasPermissionRequest{
			ResourceType: resType, ResourceUuid: resUuid, Role: role, Member: member,
		})
		Expect(err).NotTo(HaveOccurred())
		return resp.GetHasPermission()
	}
}

func listedVsUuids(ctx context.Context, api *env.Api) []string {
	GinkgoHelper()
	resp, err := api.VS.ListVS(ctx, &svcvs.ListVSRequest{})
	Expect(err).NotTo(HaveOccurred())
	uuids := make([]string, 0, len(resp.GetVs()))
	for _, virtServ := range resp.GetVs() {
		uuids = append(uuids, virtServ.GetUUID())
	}
	return uuids
}
