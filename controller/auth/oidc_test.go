package auth_test

import (
	"context"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/controller/config"
)

var _ = Describe("OidcLogin", func() {
	var provider *fakeProvider
	var conf *config.OidcLogin
	BeforeEach(func() {
		provider = newFakeProvider()
		conf = &config.OidcLogin{
			IssuerUrl:     provider.Issuer(),
			ClientId:      "flock",
			Scopes:        []string{"openid"},
			UsernameClaim: "preferred_username",
			GroupsClaim:   "groups",
		}
	})

	It("maps an ID token to an identity", func() {
		token := provider.sign(jwt.MapClaims{"aud": "flock", "preferred_username": "alice", "groups": []string{"g1"}})

		identity, err := NewOidcLogin(conf).VerifyToken(context.Background(), token)

		Expect(err).ToNot(HaveOccurred())
		Expect(identity).To(Equal(Identity{Username: "alice", Groups: []string{"g1"}}))
	})
	DescribeTable("reads the groups as today",
		func(groups any, expected []string) {
			token := provider.sign(jwt.MapClaims{"aud": "flock", "preferred_username": "alice", "groups": groups})

			identity, err := NewOidcLogin(conf).VerifyToken(context.Background(), token)

			Expect(err).ToNot(HaveOccurred())
			Expect(identity.Groups).To(ConsistOf(expected))
		},
		Entry("space separated string", "g1 g2", []string{"g1", "g2"}),
		Entry("map keys", map[string]any{"g1": true}, []string{"g1"}),
		Entry("missing", nil, []string{}),
	)
	It("fails within the provider timeout when the keys endpoint hangs", func() {
		DeferCleanup(ShortenTimeouts(500 * time.Millisecond))
		provider.hangKeys.Store(true)
		token := provider.sign(jwt.MapClaims{"aud": "flock", "preferred_username": "alice"})
		// longer than the expected bound, so a missing client timeout fails the spec instead of hanging it
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		start := time.Now()
		_, err := NewOidcLogin(conf).VerifyToken(ctx, token)

		Expect(err).To(HaveOccurred())
		Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
	})
	It("refuses an ID token without username", func() {
		token := provider.sign(jwt.MapClaims{"aud": "flock", "sub": "alice"})

		_, err := NewOidcLogin(conf).VerifyToken(context.Background(), token)

		Expect(err).To(MatchError(ContainSubstring("preferred_username")))
	})
	It("refuses an ID token for another client", func() {
		token := provider.sign(jwt.MapClaims{"aud": "other", "preferred_username": "alice"})

		_, err := NewOidcLogin(conf).VerifyToken(context.Background(), token)

		Expect(err).To(MatchError(ContainSubstring("audience")))
	})
	It("refuses a token signed by another key", func() {
		other := newFakeProvider()
		other.server.URL = provider.server.URL
		token := other.sign(jwt.MapClaims{"aud": "flock", "preferred_username": "alice"})

		_, err := NewOidcLogin(conf).VerifyToken(context.Background(), token)

		Expect(err).To(HaveOccurred())
	})
	It("accepts an access token for the configured audience", func() {
		conf.AccessToken = &config.OidcAccessToken{Audience: "flock-api", UsernameClaim: "sub"}
		token := provider.sign(jwt.MapClaims{"aud": "flock-api", "sub": "svc-ci"})

		identity, err := NewOidcLogin(conf).VerifyToken(context.Background(), token)

		Expect(err).ToNot(HaveOccurred())
		Expect(identity.Username).To(Equal("svc-ci"))
	})
	It("refuses access tokens when access_token is not configured", func() {
		token := provider.sign(jwt.MapClaims{"aud": "flock-api", "sub": "svc-ci"})

		_, err := NewOidcLogin(conf).VerifyToken(context.Background(), token)

		Expect(err).To(MatchError(ContainSubstring("audience")))
	})
	It("refuses an access token for another audience", func() {
		conf.AccessToken = &config.OidcAccessToken{Audience: "flock-api", UsernameClaim: "sub"}
		token := provider.sign(jwt.MapClaims{"aud": "other", "sub": "svc-ci"})

		_, err := NewOidcLogin(conf).VerifyToken(context.Background(), token)

		Expect(err).To(MatchError(ContainSubstring("flock-api")))
	})
	It("starts without the provider and recovers", func() {
		provider.down.Store(true)
		login := NewOidcLogin(conf)
		token := provider.sign(jwt.MapClaims{"aud": "flock", "preferred_username": "alice"})

		_, err := login.VerifyToken(context.Background(), token)
		Expect(err).To(MatchError(ErrProviderUnavailable))

		provider.down.Store(false)
		identity, err := login.VerifyToken(context.Background(), token)
		Expect(err).ToNot(HaveOccurred())
		Expect(identity.Username).To(Equal("alice"))
	})
})
