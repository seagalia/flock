//go:build e2e

package e2e_test

import (
	"time"

	"connectrpc.com/connect/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/contexes"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/seagalia/flock/e2e/controller/env"
)

var _ = Describe("Break-glass", Ordered, func() {
	var (
		g        *env.Controller
		provider *env.Oauth2
	)

	BeforeAll(func(ctx SpecContext) {
		var err error
		provider, err = e2eEnv.StartOauth2(ctx)
		Expect(err).NotTo(HaveOccurred())
		g = startController(ctx, env.ControllerOptions{Name: "controller-break-glass", Store: env.StoreSqlite, Oidc: provider})
	}, NodeTimeout(10*time.Minute))

	BeforeEach(func() {
		registerControllerForDump(g)
	})

	It("lets an admin in while the provider is down, and logs every call", func(ctx SpecContext) {
		idToken, err := provider.IdToken(ctx, "alice", env.Oauth2Users["alice"])
		Expect(err).NotTo(HaveOccurred())
		stopTimeout := 10 * time.Second
		Expect(provider.Container.Stop(ctx, &stopTimeout)).To(Succeed())
		// the controller has not discovered the provider yet, it can't check any of its tokens
		alice, err := g.LoginWithBearer(ctx, idToken)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(alice.Close)
		_, err = alice.Login.Me(ctx, &emptypb.Empty{})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnavailable), "%v", err)

		admin, err := g.LoginAsBreakGlass(ctx, "provider down in e2e")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(admin.Close)
		me, err := admin.Login.Me(ctx, &emptypb.Empty{})
		Expect(err).NotTo(HaveOccurred())
		Expect(me.GetUsername()).To(Equal(svclogin.BreakGlassUsername))
		Expect(me.GetIsAdmin()).To(BeTrue())
		_, err = admin.Zone.CreateZone(ctx, &svczone.CreateZoneRequest{Zone: env.NewZone(env.Unique("zone"))})
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() string { return g.Logs(ctx) }).Should(And(
			ContainSubstring("break-glass access"),
			ContainSubstring("provider down in e2e"),
			ContainSubstring("CreateZone"),
		))
	}, NodeTimeout(2*time.Minute))

	It("refuses an expired break-glass token", func(ctx SpecContext) {
		key, err := jwt.ParseRSAPrivateKeyFromPEM(e2eEnv.Certs.JwtPrivateKeyPem)
		Expect(err).NotTo(HaveOccurred())
		// past the leeway of the controller for clock skew
		issuedAt := time.Now().Add(-2 * time.Hour)
		claims := jwt.MapClaims{
			contexes.UsernameMetaKey:     svclogin.BreakGlassUsername,
			contexes.GroupsMetaKey:       []string{},
			"amr":                        []string{svclogin.BreakGlassAmr},
			svclogin.BreakGlassReasonKey: "expired in e2e",
			"jti":                        uuid.NewString(),
			"iss":                        env.JwtIssuer,
			"aud":                        env.JwtAudience,
			"iat":                        issuedAt.Unix(),
			"nbf":                        issuedAt.Unix(),
			"exp":                        issuedAt.Add(15 * time.Minute).Unix(),
		}
		expired, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
		Expect(err).NotTo(HaveOccurred())

		admin, err := g.LoginWithBearer(ctx, expired)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(admin.Close)
		_, err = admin.Login.Me(ctx, &emptypb.Empty{})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
		Expect(err).To(MatchError(ContainSubstring("expired")))
	}, NodeTimeout(time.Minute))
})
