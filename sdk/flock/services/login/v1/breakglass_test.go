package svclogin_test

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/flock/services/login/v1"
)

var _ = Describe("break-glass tokens", func() {
	var manager *JwtManager
	BeforeEach(func() {
		manager = newManager(hsMaker("secret"))
	})

	It("round trips the id, the reason and the expiry", func() {
		tok, expiresAt, err := manager.MakeBreakGlassToken("idp down", BreakGlassDefaultTTL)
		Expect(err).ToNot(HaveOccurred())
		Expect(expiresAt).To(BeTemporally("~", time.Now().Add(BreakGlassDefaultTTL), 5*time.Second))

		username, groups, err := manager.VerifyToken(tok)
		Expect(err).ToNot(HaveOccurred())
		Expect(username).To(Equal(BreakGlassUsername))
		Expect(groups).To(BeEmpty())

		claims, err := manager.BreakGlassClaims(tok)
		Expect(err).ToNot(HaveOccurred())
		Expect(claims.Reason).To(Equal("idp down"))
		Expect(claims.ID).ToNot(BeEmpty())
		Expect(claims.ExpiresAt).To(BeTemporally("~", expiresAt, time.Second))
	})
	It("gives each token its own id", func() {
		first, _, _ := manager.MakeBreakGlassToken("reason", time.Minute)
		second, _, _ := manager.MakeBreakGlassToken("reason", time.Minute)

		firstClaims, _ := manager.BreakGlassClaims(first)
		secondClaims, _ := manager.BreakGlassClaims(second)

		Expect(firstClaims.ID).ToNot(Equal(secondClaims.ID))
	})
	It("refuses to sign above the max ttl", func() {
		_, _, err := manager.MakeBreakGlassToken("reason", BreakGlassMaxTTL+time.Second)
		Expect(err).To(HaveOccurred())
	})
	It("refuses to sign without a ttl", func() {
		_, _, err := manager.MakeBreakGlassToken("reason", 0)
		Expect(err).To(HaveOccurred())
	})
	It("refuses to sign without a reason", func() {
		_, _, err := manager.MakeBreakGlassToken("", time.Minute)
		Expect(err).To(HaveOccurred())
	})
	It("gives no claims for a regular token", func() {
		tok, _, err := manager.MakeToken("bob", nil)
		Expect(err).ToNot(HaveOccurred())

		claims, err := manager.BreakGlassClaims(tok)

		Expect(err).ToNot(HaveOccurred())
		Expect(claims).To(BeNil())
	})
	It("refuses a break-glass token living longer than the max ttl", func() {
		now := time.Now()
		tok := signHS("secret", jwt.MapClaims{
			"username": BreakGlassUsername, "amr": []string{BreakGlassAmr}, "jti": "id",
			"iat": now.Unix(), "exp": now.Add(2 * time.Hour).Unix(),
		})

		_, err := manager.BreakGlassClaims(tok)

		Expect(err).To(HaveOccurred())
	})
	It("refuses a break-glass token without jti", func() {
		tok := signHS("secret", jwt.MapClaims{"username": BreakGlassUsername, "amr": []string{BreakGlassAmr}})

		_, err := manager.BreakGlassClaims(tok)

		Expect(err).To(HaveOccurred())
	})
})
