package serves_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/controller/lockers"
	. "github.com/seagalia/flock/controller/serves"
	sdkconfig "github.com/seagalia/flock/sdk/config"
	"github.com/seagalia/flock/sdk/contexes"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/types/known/emptypb"
)

func newJwtManager() *svclogin.JwtManager {
	GinkgoHelper()
	jwtMan, err := svclogin.NewJwtManager(&sdkconfig.JwtMaker{
		Alg:        sdkconfig.JwtAlg{AlgRaw: "HS256", Alg: jwt.SigningMethodHS256},
		PrivateKey: "secret",
		ExpireIn:   sdkconfig.Duration(3 * time.Hour),
		Issuer:     "flock",
		Audience:   "flock",
	})
	Expect(err).ToNot(HaveOccurred())
	return jwtMan
}

func newLoginServer(jwtMan *svclogin.JwtManager, loginConf *config.ServerLogin) *FlockServer {
	loginConf.LockoutAfterFailures = 3
	loginConf.LockoutDuration = sdkconfig.Duration(time.Minute)
	login, err := auth.NewLogin(loginConf)
	Expect(err).ToNot(HaveOccurred())
	admConf := config.AdminConfig{Users: []string{adminUser}}
	return NewFlockServer(context.Background(), nil, admConf, nil, storers.NewMemoryManager(), jwtMan, login, nil, nil, nil, lockers.NewMemory(), nil)
}

func basicLogin() *config.ServerLogin {
	return &config.ServerLogin{
		Type:      "basic",
		BasicAuth: []*config.BasicAuthLogin{{Username: "bob", Password: "pass", Groups: []string{"team"}}},
	}
}

// oidcProvider is an OIDC provider serving its discovery document and its keys, it answers 503 while down
type oidcProvider struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	down   atomic.Bool
	// requests counts every call received, down or not
	requests atomic.Int32
}

func newOidcProvider() *oidcProvider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).ToNot(HaveOccurred())
	provider := &oidcProvider{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		if provider.down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": provider.server.URL, "jwks_uri": provider.server.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "key-1", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	provider.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider.requests.Add(1)
		mux.ServeHTTP(w, r)
	}))
	DeferCleanup(provider.server.Close)
	return provider
}

func (p *oidcProvider) idToken(username string) string {
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": p.server.URL, "aud": "flock", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"preferred_username": username, "groups": []string{"idp-team"},
	})
	tok.Header["kid"] = "key-1"
	signed, err := tok.SignedString(p.key)
	Expect(err).ToNot(HaveOccurred())
	return signed
}

func (p *oidcProvider) login() *config.ServerLogin {
	return &config.ServerLogin{Type: "oidc", Oidc: &config.OidcLogin{
		IssuerUrl: p.server.URL, ClientId: "flock", UsernameClaim: "preferred_username", GroupsClaim: "groups",
	}}
}

func bearerCtx(token string) context.Context {
	return withHeaders(context.Background(), "authorization", "Bearer "+token)
}

var _ = Describe("Login", func() {
	var server *FlockServer
	var jwtMan *svclogin.JwtManager
	BeforeEach(func() {
		jwtMan = newJwtManager()
		server = newLoginServer(jwtMan, basicLogin())
	})

	Context("GetToken", func() {
		It("should give a token holding user info", func() {
			resp, err := server.GetToken(context.Background(), &svclogin.GetTokenRequest{Username: "bob", Password: "pass"})
			Expect(err).ToNot(HaveOccurred())

			Expect(resp.GetToken()).ToNot(BeEmpty())
			Expect(resp.GetExpiresAt().AsTime()).To(BeTemporally("~", time.Now().Add(3*time.Hour), time.Minute))
			username, groups, err := jwtMan.VerifyToken(resp.GetToken())
			Expect(err).ToNot(HaveOccurred())
			Expect(username).To(Equal("bob"))
			Expect(groups).To(Equal([]string{"team"}))
		})
		It("refuses the reserved username break-glass", func() {
			loginConf := basicLogin()
			loginConf.BasicAuth = append(loginConf.BasicAuth, &config.BasicAuthLogin{Username: "break-glass", Password: "pass"})
			server = newLoginServer(jwtMan, loginConf)

			_, err := server.GetToken(context.Background(), &svclogin.GetTokenRequest{Username: "break-glass", Password: "pass"})

			expectCode(err, connect.CodeUnauthenticated)
			Expect(err).To(MatchError(ContainSubstring("username break-glass is reserved")))
		})
		It("should refuse bad credentials", func() {
			_, err := server.GetToken(context.Background(), &svclogin.GetTokenRequest{Username: "bob", Password: "bad"})
			expectCode(err, connect.CodeUnauthenticated)
		})
		It("refuses GetToken on an OIDC controller", func() {
			server = newLoginServer(jwtMan, newOidcProvider().login())

			_, err := server.GetToken(context.Background(), &svclogin.GetTokenRequest{Username: "bob", Password: "pass"})

			expectCode(err, connect.CodeFailedPrecondition)
			Expect(err).To(MatchError(ContainSubstring("this controller logs in with OIDC, get a token from the provider")))
		})
	})

	Context("GetServiceAuth", func() {
		It("gives the type of a basic login and no provider", func() {
			resp, err := server.GetServiceAuth(context.Background(), &emptypb.Empty{})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetType()).To(Equal("BASIC"))
			Expect(resp.GetOidc()).To(BeNil())
		})
		It("gives the provider of an OIDC login", func() {
			provider := newOidcProvider()
			loginConf := provider.login()
			loginConf.Oidc.Scopes = []string{"openid", "groups"}
			server = newLoginServer(jwtMan, loginConf)

			resp, err := server.GetServiceAuth(context.Background(), &emptypb.Empty{})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetType()).To(Equal("OIDC"))
			Expect(resp.GetOidc().GetIssuerUrl()).To(Equal(provider.server.URL))
			Expect(resp.GetOidc().GetClientId()).To(Equal("flock"))
			Expect(resp.GetOidc().GetScopes()).To(Equal([]string{"openid", "groups"}))
		})
	})

	Context("AuthFuncOverride", func() {
		BeforeEach(func() {
			// suite runs in test mode where every call is considered as coming from an agent
			auth.SetTestMode(false)
			DeferCleanup(auth.SetTestMode, true)
		})
		tokenCtx := func(username string) context.Context {
			token, _, err := jwtMan.MakeToken(username, []string{"team"})
			Expect(err).ToNot(HaveOccurred())
			return withHeaders(context.Background(), "authorization", "bearer "+token)
		}

		It("accepts a flock token", func() {
			resp, err := server.GetToken(context.Background(), &svclogin.GetTokenRequest{Username: "bob", Password: "pass"})
			Expect(err).ToNot(HaveOccurred())

			ctx, err := server.AuthFuncOverride(bearerCtx(resp.GetToken()), "/flock.services.backend.v1.Backend/ListBackend")

			Expect(err).ToNot(HaveOccurred())
			username, groups, ok := contexes.RetrieveUserInfo(ctx)
			Expect(ok).To(BeTrue())
			Expect(username).To(Equal("bob"))
			Expect(groups).To(Equal([]string{"team"}))
		})
		It("accepts a flock token on an OIDC controller", func() {
			server = newLoginServer(jwtMan, newOidcProvider().login())

			ctx, err := server.AuthFuncOverride(tokenCtx("bob"), "/flock.services.backend.v1.Backend/ListBackend")

			Expect(err).ToNot(HaveOccurred())
			username, _, _ := contexes.RetrieveUserInfo(ctx)
			Expect(username).To(Equal("bob"))
		})
		It("accepts an OIDC ID token", func() {
			provider := newOidcProvider()
			server = newLoginServer(jwtMan, provider.login())

			ctx, err := server.AuthFuncOverride(bearerCtx(provider.idToken("alice")), "/flock.services.backend.v1.Backend/ListBackend")

			Expect(err).ToNot(HaveOccurred())
			username, groups, ok := contexes.RetrieveUserInfo(ctx)
			Expect(ok).To(BeTrue())
			Expect(username).To(Equal("alice"))
			Expect(groups).To(Equal([]string{"idp-team"}))
		})
		It("refuses an OIDC token on a basic controller", func() {
			provider := newOidcProvider()

			_, err := server.AuthFuncOverride(bearerCtx(provider.idToken("alice")), "/flock.services.backend.v1.Backend/ListBackend")

			expectCode(err, connect.CodeUnauthenticated)
			Expect(err).To(MatchError(ContainSubstring("unknown token issuer " + provider.server.URL)))
		})
		It("names an unknown issuer", func() {
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
				"iss": "https://evil", "username": adminUser, "exp": time.Now().Add(time.Hour).Unix(),
			}).SignedString([]byte("secret"))
			Expect(err).ToNot(HaveOccurred())

			_, err = server.AuthFuncOverride(bearerCtx(token), "/flock.services.backend.v1.Backend/ListBackend")

			expectCode(err, connect.CodeUnauthenticated)
			Expect(err).To(MatchError(ContainSubstring("unknown token issuer https://evil")))
		})
		It("answers Unavailable while the provider is down", func() {
			provider := newOidcProvider()
			provider.down.Store(true)
			server = newLoginServer(jwtMan, provider.login())

			_, err := server.AuthFuncOverride(bearerCtx(provider.idToken("alice")), "/flock.services.backend.v1.Backend/ListBackend")

			expectCode(err, connect.CodeUnavailable)
		})
		It("should refuse a call claiming mtls through headers", func() {
			ctx := withHeaders(context.Background(), "is_mtls", "true", "username", adminUser)
			_, err := server.AuthFuncOverride(ctx, "/flock.services.zone.v1.Zone/CreateZone")
			expectCode(err, connect.CodeUnauthenticated)
		})
		It("should ignore identity headers sent next to a valid token", func() {
			ctx, err := server.AuthFuncOverride(withHeaders(tokenCtx("bob"), "username", adminUser), "/flock.services.backend.v1.Backend/ListBackend")
			Expect(err).ToNot(HaveOccurred())
			username, _, _ := contexes.RetrieveUserInfo(ctx)
			Expect(username).To(Equal("bob"))
		})
		It("should ignore the id_token header", func() {
			adminToken, _, err := jwtMan.MakeToken(adminUser, nil)
			Expect(err).ToNot(HaveOccurred())

			ctx, err := server.AuthFuncOverride(withHeaders(tokenCtx("bob"), "id_token", adminToken), "/flock.services.backend.v1.Backend/ListBackend")

			Expect(err).ToNot(HaveOccurred())
			username, _, _ := contexes.RetrieveUserInfo(ctx)
			Expect(username).To(Equal("bob"))
		})
		It("should allow login service without token", func() {
			_, err := server.AuthFuncOverride(context.Background(), "/flock.services.login.v1.Login/GetToken")
			Expect(err).ToNot(HaveOccurred())
		})
		It("should refuse call without token", func() {
			_, err := server.AuthFuncOverride(context.Background(), "/flock.services.backend.v1.Backend/ListBackend")
			expectCode(err, connect.CodeUnauthenticated)
		})
		DescribeTable("should refuse a malformed authorization",
			func(authorization string) {
				ctx := withHeaders(context.Background(), "authorization", authorization)

				_, err := server.AuthFuncOverride(ctx, "/flock.services.backend.v1.Backend/ListBackend")

				expectCode(err, connect.CodeUnauthenticated)
			},
			Entry("no scheme", "token"),
			Entry("another scheme", "Basic dXNlcjpwYXNz"),
			Entry("not a jwt", "bearer invalid"),
			Entry("no issuer", "bearer "+func() string {
				token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"username": "bob"}).SignedString([]byte("secret"))
				return token
			}()),
		)
		Context("break-glass", func() {
			const method = "/flock.services.backend.v1.Backend/ListBackend"
			breakGlassCtx := func(reason string, ttl time.Duration) context.Context {
				token, _, err := jwtMan.MakeBreakGlassToken(reason, ttl)
				Expect(err).ToNot(HaveOccurred())
				return bearerCtx(token)
			}

			It("lets a break-glass token in as admin", func() {
				ctx, err := server.AuthFuncOverride(breakGlassCtx("idp down", time.Minute), method)

				Expect(err).ToNot(HaveOccurred())
				username, _, _ := contexes.RetrieveUserInfo(ctx)
				Expect(username).To(Equal(svclogin.BreakGlassUsername))
				resp, err := server.Me(breakGlassCtx("idp down", time.Minute), &emptypb.Empty{})
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetIsAdmin()).To(BeTrue())
			})
			It("accepts break-glass while the OIDC provider is down and never calls it", func() {
				provider := newOidcProvider()
				provider.down.Store(true)
				server = newLoginServer(jwtMan, provider.login())

				_, err := server.AuthFuncOverride(breakGlassCtx("idp down", time.Minute), method)

				Expect(err).ToNot(HaveOccurred())
				Expect(provider.requests.Load()).To(BeZero())
			})
			It("refuses an expired break-glass token", func() {
				now := time.Now()
				token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
					"username": svclogin.BreakGlassUsername, "amr": []string{svclogin.BreakGlassAmr}, "jti": "id",
					"iss": "flock", "aud": "flock",
					"iat": now.Add(-2 * time.Hour).Unix(), "exp": now.Add(-time.Hour).Unix(),
				}).SignedString([]byte("secret"))
				Expect(err).ToNot(HaveOccurred())

				_, err = server.AuthFuncOverride(bearerCtx(token), method)

				expectCode(err, connect.CodeUnauthenticated)
			})
			It("refuses the reserved username without break-glass claims", func() {
				token, _, err := jwtMan.MakeToken(svclogin.BreakGlassUsername, nil)
				Expect(err).ToNot(HaveOccurred())

				_, err = server.AuthFuncOverride(bearerCtx(token), method)

				expectCode(err, connect.CodeUnauthenticated)
				Expect(err).To(MatchError(ContainSubstring("username break-glass is reserved")))
			})
			It("refuses an agent whose name spells the reserved username", func() {
				// agent names are the client address (a header client can set) and the certificate CN
				serverTLS, clientTLS := rpctest.NewTLS("glass")
				ctx, err := rpctest.ServerContextOverHTTP(serverTLS, clientTLS, map[string]string{"X-Real-IP": "break"})
				Expect(err).ToNot(HaveOccurred())

				_, err = server.AuthFuncOverride(ctx, method)

				expectCode(err, connect.CodeUnauthenticated)
				Expect(err).To(MatchError(ContainSubstring("username break-glass is reserved")))
			})
			It("grants no admin to the reserved username without the break-glass marker", func() {
				ctx := contexes.WithUserInfo(context.Background(), svclogin.BreakGlassUsername, nil)

				_, err := server.CreateZone(ctx, &svczone.CreateZoneRequest{})

				expectCode(err, connect.CodePermissionDenied)
			})
			It("refuses the reserved username from an OIDC login", func() {
				provider := newOidcProvider()
				server = newLoginServer(jwtMan, provider.login())

				_, err := server.AuthFuncOverride(bearerCtx(provider.idToken(svclogin.BreakGlassUsername)), method)

				expectCode(err, connect.CodeUnauthenticated)
				Expect(err).To(MatchError(ContainSubstring("username break-glass is reserved")))
			})
			It("logs every break-glass call", func() {
				logs := &bytes.Buffer{}
				previous := log.Logger
				log.Logger = zerolog.New(logs)
				DeferCleanup(func() { log.Logger = previous })
				server = newLoginServer(jwtMan, basicLogin())
				ctx := breakGlassCtx("idp down", time.Minute)

				_, err := server.AuthFuncOverride(ctx, method)
				Expect(err).ToNot(HaveOccurred())
				_, err = server.AuthFuncOverride(ctx, method)
				Expect(err).ToNot(HaveOccurred())

				var warnLines []map[string]any
				for line := range bytes.SplitSeq(bytes.TrimSpace(logs.Bytes()), []byte("\n")) {
					entry := map[string]any{}
					Expect(json.Unmarshal(line, &entry)).To(Succeed())
					if entry["message"] == "break-glass access" {
						warnLines = append(warnLines, entry)
					}
				}
				Expect(warnLines).To(HaveLen(2))
				Expect(warnLines[0]).To(HaveKeyWithValue("level", "warn"))
				Expect(warnLines[0]).To(HaveKeyWithValue("reason", "idp down"))
				Expect(warnLines[0]).To(HaveKeyWithValue("method", method))
				Expect(warnLines[0]).To(HaveKey("break_glass_id"))
				Expect(warnLines[0]).To(HaveKey("expires_at"))
			})
		})
		It("should give info about current user", func() {
			resp, err := server.Me(tokenCtx(adminUser), &emptypb.Empty{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetUsername()).To(Equal(adminUser))
			Expect(resp.GetGroups()).To(Equal([]string{"team"}))
			Expect(resp.GetIsAdmin()).To(BeTrue())
		})
	})
})
