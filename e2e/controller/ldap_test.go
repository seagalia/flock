//go:build e2e

package e2e_test

import (
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/seagalia/flock/e2e/controller/env"
)

var _ = Describe("LDAP", Ordered, func() {
	var (
		g                       *env.Controller
		alice, bob, carol, root *env.Api
	)

	BeforeAll(func(ctx SpecContext) {
		ldapServer, err := e2eEnv.StartLdap(ctx)
		Expect(err).NotTo(HaveOccurred())
		g = startController(ctx, env.ControllerOptions{Name: "controller-ldap", Store: env.StoreSqlite, Ldap: ldapServer})
		alice = loginAs(ctx, g, "ldap-alice", env.LdapUserPassword)
		bob = loginAs(ctx, g, "ldap-bob", env.LdapUserPassword)
		carol = loginAs(ctx, g, "ldap-carol", env.LdapUserPassword)
		root = loginAs(ctx, g, "ldap-root", env.LdapUserPassword)
	}, NodeTimeout(10*time.Minute))

	BeforeEach(func() {
		registerControllerForDump(g)
	})

	It("logs a user in with the groups it is a member of", func(ctx SpecContext) {
		me, err := bob.Login.Me(ctx, &emptypb.Empty{})
		Expect(err).NotTo(HaveOccurred())
		Expect(me.GetUsername()).To(Equal("ldap-bob"))
		Expect(me.GetGroups()).To(ConsistOf("team-ldap", "team-bob"))

		me, err = carol.Login.Me(ctx, &emptypb.Empty{})
		Expect(err).NotTo(HaveOccurred())
		Expect(me.GetGroups()).To(BeEmpty())
	}, NodeTimeout(time.Minute))

	It("refuses a wrong password and an unknown user", func(ctx SpecContext) {
		_, err := g.LoginAs(ctx, "ldap-alice", "wrong-password")
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)

		_, err = g.LoginAs(ctx, "ldap-nobody", env.LdapUserPassword)
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
	}, NodeTimeout(time.Minute))

	It("grants through the ldap groups", func(ctx SpecContext) {
		backendUuid := createBackend(ctx, alice)
		_, err := bob.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)

		grant(ctx, alice, backendType, backendUuid, permission.Role_READER, group("team-bob"))

		Eventually(func() error {
			_, err := bob.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
			return err
		}).Should(Succeed())
		_, err = carol.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%v", err)
	}, NodeTimeout(time.Minute))

	It("makes the members of the admin group admins", func(ctx SpecContext) {
		privateVsUuid, _ := createTcpVs(ctx, alice, 8080)

		Expect(listedVsUuids(ctx, root)).To(ContainElement(privateVsUuid))
		Expect(listedVsUuids(ctx, carol)).NotTo(ContainElement(privateVsUuid))
	}, NodeTimeout(time.Minute))

	It("refuses a username matching another user through the search filter", func(ctx SpecContext) {
		_, err := g.LoginAs(ctx, "ldap-ali*", env.LdapUserPassword)
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
	}, NodeTimeout(time.Minute))

	It("locks a user out after too many wrong passwords", func(ctx SpecContext) {
		for range 6 {
			_, err := g.LoginAs(ctx, "ldap-carol", "wrong-password")
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
		}

		_, err := g.LoginAs(ctx, "ldap-carol", env.LdapUserPassword)
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
	}, NodeTimeout(time.Minute))
})
