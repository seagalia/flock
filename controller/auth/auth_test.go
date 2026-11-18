package auth_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/controller/config"
	sdkconfig "github.com/seagalia/flock/sdk/config"
)

var _ = Describe("NewLogin", func() {
	lockout := func(conf *config.ServerLogin) *config.ServerLogin {
		conf.LockoutAfterFailures = 1
		conf.LockoutDuration = sdkconfig.Duration(time.Minute)
		return conf
	}

	It("builds a basic login protected by the lockout", func() {
		login, err := NewLogin(lockout(&config.ServerLogin{
			Type:      "basic",
			BasicAuth: []*config.BasicAuthLogin{{Username: "user", Password: "pass"}},
		}))
		Expect(err).ToNot(HaveOccurred())
		Expect(login.Type()).To(Equal(string(AuthTypeBasic)))
		Expect(login).To(BeAssignableToTypeOf(&Protect{}))

		userLogin, ok := login.(AuthUserPassword)
		Expect(ok).To(BeTrue())
		_, _, err = userLogin.LoginUser("user", "bad")(context.Background())
		Expect(err).ToNot(HaveOccurred())
		_, _, err = userLogin.LoginUser("user", "bad")(context.Background())
		Expect(err).To(MatchError(ContainSubstring("locked-out")))
	})
	It("builds a ldap login protected by the lockout", func() {
		login, err := NewLogin(lockout(&config.ServerLogin{Type: "ldap", Ldap: &config.LdapLogin{Address: "127.0.0.1:1"}}))
		Expect(err).ToNot(HaveOccurred())
		Expect(login.Type()).To(Equal(string(AuthTypeLdap)))
		Expect(login).To(BeAssignableToTypeOf(&Protect{}))
	})
	It("builds an oidc login verifying tokens", func() {
		login, err := NewLogin(&config.ServerLogin{
			Type: "oidc",
			Oidc: &config.OidcLogin{IssuerUrl: "https://idp/r", ClientId: "flock"},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(login.Type()).To(Equal(string(AuthTypeOidc)))
		verifier, ok := login.(TokenVerifier)
		Expect(ok).To(BeTrue())
		Expect(verifier.Issuer()).To(Equal("https://idp/r"))
		_, isUserLogin := login.(AuthUserPassword)
		Expect(isUserLogin).To(BeFalse())
	})
	It("refuses an unknown type", func() {
		_, err := NewLogin(&config.ServerLogin{Type: "kerberos"})
		Expect(err).To(MatchError(ContainSubstring("unknown login type kerberos")))
	})
})
