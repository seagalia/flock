package svclogin_test

import (
	"maps"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/sdk/config"
	. "github.com/seagalia/flock/sdk/flock/services/login/v1"
)

func algOf(name string) config.JwtAlg {
	return config.JwtAlg{AlgRaw: name, Alg: jwt.GetSigningMethod(name)}
}

func hsMaker(secret string) *config.JwtMaker { //nolint:unparam // the call sites show the value
	return &config.JwtMaker{
		Alg:        algOf("HS256"),
		PrivateKey: secret,
		ExpireIn:   config.Duration(time.Hour),
		Issuer:     "flock",
		Audience:   "flock",
	}
}

func newManager(maker *config.JwtMaker) *JwtManager {
	GinkgoHelper()
	manager, err := NewJwtManager(maker)
	Expect(err).ToNot(HaveOccurred())
	return manager
}

// given claims override valid defaults for username, issuer, audience and dates
func signHS(secret string, claims jwt.MapClaims) string {
	now := time.Now()
	full := jwt.MapClaims{
		"username": "user", "iss": "flock", "aud": "flock", "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	maps.Copy(full, claims)
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, full).SignedString([]byte(secret))
	Expect(err).ToNot(HaveOccurred())
	return tok
}

var _ = Describe("JwtManager", func() {
	DescribeTable("should verify the tokens it made",
		func(alg string, privatePem string) {
			maker := &config.JwtMaker{
				Alg: algOf(alg), PrivateKey: privatePem, ExpireIn: config.Duration(time.Hour), Issuer: "flock", Audience: "flock",
			}
			manager := newManager(maker)

			tok, expireAt, err := manager.MakeToken("user", []string{"admin", "dev"})
			Expect(err).ToNot(HaveOccurred())
			Expect(expireAt).To(BeTemporally("~", time.Now().Add(time.Hour), 5*time.Second))

			username, groups, err := manager.VerifyToken(tok)
			Expect(err).ToNot(HaveOccurred())
			Expect(username).To(Equal("user"))
			Expect(groups).To(Equal([]string{"admin", "dev"}))
		},
		func() []TableEntry {
			rsaPrivate, _ := rsaKeyPairPem()
			ecPrivate, _ := ecKeyPairPem()
			return []TableEntry{
				Entry("hmac", "HS256", "secret"),
				Entry("rsa", "RS256", rsaPrivate),
				Entry("ecdsa", "ES256", ecPrivate),
			}
		}(),
	)

	It("should refuse a key that does not parse", func() {
		maker := &config.JwtMaker{Alg: algOf("RS256"), PrivateKey: "not a pem", Issuer: "flock"}

		_, err := NewJwtManager(maker)

		Expect(err).To(MatchError(ContainSubstring("jwt_maker private key for RS256")))
	})

	It("should give the issuer of its tokens", func() {
		Expect(newManager(hsMaker("secret")).Issuer()).To(Equal("flock"))
	})

	It("should accept tokens without audience when the maker has none", func() {
		maker := hsMaker("secret")
		maker.Audience = ""
		manager := newManager(maker)
		tok, _, err := manager.MakeToken("user", nil)
		Expect(err).ToNot(HaveOccurred())
		_, _, err = manager.VerifyToken(tok)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should accept a clock skew of a few minutes", func() {
		tok := signHS("secret", jwt.MapClaims{"iat": time.Now().Add(2 * time.Minute).Unix()})
		_, _, err := newManager(hsMaker("secret")).VerifyToken(tok)
		Expect(err).ToNot(HaveOccurred())
	})

	DescribeTable("should refuse invalid tokens",
		func(makeTok func() string) {
			_, _, err := newManager(hsMaker("secret")).VerifyToken(makeTok())
			Expect(err).To(HaveOccurred())
		},
		Entry("signed with another key", func() string { return signHS("other", nil) }),
		Entry("from another issuer", func() string { return signHS("secret", jwt.MapClaims{"iss": "other"}) }),
		Entry("for another audience", func() string { return signHS("secret", jwt.MapClaims{"aud": "other"}) }),
		Entry("expired", func() string {
			return signHS("secret", jwt.MapClaims{"exp": time.Now().Add(-time.Hour).Unix()})
		}),
		Entry("not valid yet", func() string {
			return signHS("secret", jwt.MapClaims{"nbf": time.Now().Add(time.Hour).Unix()})
		}),
		Entry("issued in the future", func() string {
			return signHS("secret", jwt.MapClaims{"iat": time.Now().Add(time.Hour).Unix()})
		}),
		Entry("without username", func() string { return signHS("secret", jwt.MapClaims{"username": 42}) }),
		Entry("not a jwt", func() string { return "not a jwt" }),
	)

	It("should refuse a token signed by another rsa key", func() {
		privatePem, _ := rsaKeyPairPem()
		otherPem, _ := rsaKeyPairPem()
		maker := &config.JwtMaker{Alg: algOf("RS256"), PrivateKey: privatePem, ExpireIn: config.Duration(time.Hour), Issuer: "flock"}
		other := &config.JwtMaker{Alg: algOf("RS256"), PrivateKey: otherPem, ExpireIn: config.Duration(time.Hour), Issuer: "flock"}
		tok, _, err := newManager(other).MakeToken("user", nil)
		Expect(err).ToNot(HaveOccurred())

		_, _, err = newManager(maker).VerifyToken(tok)
		Expect(err).To(HaveOccurred())
	})

	It("should refuse a token signed with another algorithm", func() {
		tok := signHS("secret", nil)
		privatePem, _ := rsaKeyPairPem()
		maker := &config.JwtMaker{Alg: algOf("RS256"), PrivateKey: privatePem, ExpireIn: config.Duration(time.Hour), Issuer: "flock"}
		_, _, err := newManager(maker).VerifyToken(tok)
		Expect(err).To(HaveOccurred())
	})
})
