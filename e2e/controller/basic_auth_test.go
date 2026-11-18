//go:build e2e

package e2e_test

import (
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/seagalia/flock/e2e/controller/env"
)

var _ = Describe("Basic auth", func() {
	Describe("refusals", Ordered, func() {
		var g *env.Controller

		BeforeAll(func(ctx SpecContext) {
			g = startController(ctx, env.ControllerOptions{Name: "controller-basic", Store: env.StoreSqlite})
		}, NodeTimeout(10*time.Minute))

		BeforeEach(func() {
			registerControllerForDump(g)
		})

		It("refuses a wrong password and an unknown user", func(ctx SpecContext) {
			_, err := g.LoginAs(ctx, env.AdminUser, "wrong-password")
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
			Expect(err).To(MatchError(ContainSubstring("invalid credentials")))

			_, err = g.LoginAs(ctx, "nobody", env.AdminPassword)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
		}, NodeTimeout(time.Minute))

		It("logs the admin in with its password", func(ctx SpecContext) {
			admin := loginAs(ctx, g, env.AdminUser, env.AdminPassword)

			me, err := admin.Login.Me(ctx, &emptypb.Empty{})
			Expect(err).NotTo(HaveOccurred())
			Expect(me.GetUsername()).To(Equal(env.AdminUser))
			Expect(me.GetIsAdmin()).To(BeTrue())
		}, NodeTimeout(time.Minute))

		It("refuses a call without token or with a forged one", func(ctx SpecContext) {
			anonymous, err := g.Anonymous(ctx)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(anonymous.Close)
			_, err = anonymous.Backend.ListBackend(ctx, &svcbackend.ListBackendRequest{})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
			_, err = anonymous.Login.Me(ctx, &emptypb.Empty{})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)

			forged, err := g.LoginWithBearer(ctx, "forged")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(forged.Close)
			_, err = forged.Backend.ListBackend(ctx, &svcbackend.ListBackendRequest{})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
		}, NodeTimeout(time.Minute))
	})

	// its own controller: the lockout of the admin would refuse the logins of any later spec
	It("locks a user out after too many failures", func(ctx SpecContext) {
		g := startController(ctx, env.ControllerOptions{Name: "controller-basic-lockout", Store: env.StoreSqlite})
		// lockout_after_failures defaults to 5, the failure after them locks the user out
		for range 6 {
			_, err := g.LoginAs(ctx, env.AdminUser, "wrong-password")
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
		}

		_, err := g.LoginAs(ctx, env.AdminUser, env.AdminPassword)
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
		Expect(g.Logs(ctx)).To(ContainSubstring("locked-out"))
	}, NodeTimeout(10*time.Minute))
})
