package auth_test

import (
	"context"
	"net"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/controller/config"
)

var _ = Describe("BasicAuth", func() {
	var basic *BasicAuth
	BeforeEach(func() {
		basic = NewBasicAuth([]*config.BasicAuthLogin{
			{Username: "user1", Password: "pass1", Groups: []string{"g1"}},
			{Username: "user2", Password: "pass2", Groups: []string{"g2"}},
		})
	})

	It("should log user with its groups", func() {
		loginInfo, ok, err := basic.LoginUser("user2", "pass2")(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(loginInfo.Username).To(Equal("user2"))
		Expect(loginInfo.Groups).To(Equal([]string{"g2"}))
	})
	It("should refuse wrong password", func() {
		_, ok, err := basic.LoginUser("user1", "pass2")(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse())
	})
	It("should refuse unknown user", func() {
		_, ok, err := basic.LoginUser("unknown", "pass1")(context.Background())
		Expect(err).ToNot(HaveOccurred())
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("LdapAuth", func() {
	When("ldap server accepts the connection and never answers", func() {
		It("should fail within the ldap timeout", func() {
			DeferCleanup(ShortenTimeouts(500 * time.Millisecond))
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(listener.Close)
			// mutex protects held as the accept goroutine appends while the cleanup closes
			var mutex sync.Mutex
			var held []net.Conn
			DeferCleanup(func() {
				mutex.Lock()
				defer mutex.Unlock()
				for _, conn := range held {
					conn.Close() // nolint:errcheck
				}
			})
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					mutex.Lock()
					held = append(held, conn)
					mutex.Unlock()
				}
			}()
			ldap := NewLdapAuth(&config.LdapLogin{Address: listener.Addr().String()})

			// longer than the expected bound, so a missing timeout fails the spec instead of hanging it
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			start := time.Now()
			_, ok, err := ldap.LoginUser("user", "pass")(ctx)

			Expect(ok).To(BeFalse())
			Expect(err).To(HaveOccurred())
			Expect(ctx.Err()).ToNot(HaveOccurred())
			Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
		})
		It("should stop when the context is cancelled", func() {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(listener.Close)
			ldap := NewLdapAuth(&config.LdapLogin{Address: listener.Addr().String()})
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			start := time.Now()
			_, _, err = ldap.LoginUser("user", "pass")(ctx)

			Expect(err).To(HaveOccurred())
			Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
		})
	})
	When("ldap server is unreachable", func() {
		It("should return an error", func() {
			ldap := NewLdapAuth(&config.LdapLogin{Address: "127.0.0.1:1"})

			_, ok, err := ldap.LoginUser("user", "pass")(context.Background())

			Expect(ok).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("invalid ldap for '127.0.0.1:1'")))
		})
	})
})
