package contexes_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/sdk/contexes"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
)

var _ = Describe("Context", func() {
	incoming := func(kv ...string) context.Context {
		header := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			header[kv[i]] = kv[i+1]
		}
		return rpctest.ServerContext(context.Background(), header)
	}

	Context("Retrieving auth headers", func() {
		It("should give the authorization header", func() {
			Expect(RetrieveAuthorization(incoming("authorization", "bearer tok"))).To(Equal("bearer tok"))
		})

		It("should fail when the header is missing", func() {
			_, err := RetrieveAuthorization(incoming("other", "value"))
			Expect(err).To(HaveOccurred())
			_, err = RetrieveAuthorization(context.Background())
			Expect(err).To(HaveOccurred())
		})
	})

	Context("RetrieveUserInfo", func() {
		It("should give user info set with WithUserInfo", func() {
			username, groups, ok := RetrieveUserInfo(WithUserInfo(context.Background(), "user", []string{"admin", "dev"}))
			Expect(ok).To(BeTrue())
			Expect(username).To(Equal("user"))
			Expect(groups).To(Equal([]string{"admin", "dev"}))
		})

		It("should ignore user info sent in headers", func() {
			_, _, ok := RetrieveUserInfo(incoming(UsernameMetaKey, "admin", GroupsMetaKey, "admins"))
			Expect(ok).To(BeFalse())
		})

		It("should tell when no user info is set", func() {
			_, _, ok := RetrieveUserInfo(context.Background())
			Expect(ok).To(BeFalse())
		})
	})
})
