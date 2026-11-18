package auth_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/controller/config"
)

var _ = Describe("Protect", func() {
	var protect *Protect
	lockoutFor := 200 * time.Millisecond
	BeforeEach(func() {
		basic := NewBasicAuth([]*config.BasicAuthLogin{
			{Username: "user", Password: "pass"},
		})
		protect = NewProtect(basic, 2, lockoutFor)
	})

	It("should log user with good credentials", func() {
		loginInfo, ok, err := protect.LoginUser("user", "pass")(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(loginInfo.Username).To(Equal("user"))
	})
	It("should refuse bad credentials without locking before reaching max failures", func() {
		for range 2 {
			_, ok, err := protect.LoginUser("user", "bad")(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeFalse())
		}

		_, ok, err := protect.LoginUser("user", "pass")(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
	})
	When("max failures are exceeded", func() {
		BeforeEach(func() {
			for range 2 {
				_, _, err := protect.LoginUser("user", "bad")(context.Background())
				Expect(err).ToNot(HaveOccurred())
			}
			_, ok, err := protect.LoginUser("user", "bad")(context.Background())
			Expect(ok).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("User user is locked-out")))
		})
		It("should lock user even with good credentials", func() {
			_, ok, err := protect.LoginUser("user", "pass")(context.Background())
			Expect(ok).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("User user is locked-out")))
		})
		It("should not lock other users", func() {
			_, ok, err := protect.LoginUser("other", "bad")(context.Background())
			Expect(ok).To(BeFalse())
			Expect(err).ToNot(HaveOccurred())
		})
		It("should unlock user after lockout duration", func() {
			Eventually(func() bool {
				_, ok, _ := protect.LoginUser("user", "pass")(context.Background())
				return ok
			}).WithTimeout(2 * time.Second).WithPolling(50 * time.Millisecond).Should(BeTrue())
		})
	})
	When("protected auth returns an error", func() {
		It("should return the error and reset failure count", func() {
			protect = NewProtect(NewLdapAuth(&config.LdapLogin{Address: "127.0.0.1:1"}), 1, lockoutFor)

			for range 3 {
				_, ok, err := protect.LoginUser("user", "pass")(context.Background())
				Expect(ok).To(BeFalse())
				Expect(err).To(MatchError(ContainSubstring("invalid ldap")))
			}
		})
	})
})
