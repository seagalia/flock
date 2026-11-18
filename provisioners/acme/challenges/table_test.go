package challenges_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/provisioners/acme/challenges"
)

var _ = Describe("Table", func() {
	var table *challenges.Table

	BeforeEach(func() {
		table = challenges.NewTable()
	})

	It("round-trips challenges through the secret data", func() {
		input := []challenges.Challenge{{Domain: "a.test", Token: "tok-1", KeyAuth: "ka-1"}}
		data := challenges.EncodeData(input)

		Expect(challenges.DecodeData(data)).To(Equal(input))
		for key := range data {
			Expect(key).To(MatchRegexp(`^[A-Za-z0-9._-]{1,128}$`))
		}
	})

	It("skips the keys and values that do not decode", func() {
		data := challenges.EncodeData([]challenges.Challenge{{Domain: "a.test", Token: "tok-1", KeyAuth: "ka-1"}})
		data["c.broken"] = "not json"
		data["other"] = `{"domain":"x","key_auth":"y"}`

		Expect(challenges.DecodeData(data)).To(HaveLen(1))
	})

	It("answers a token of any secret", func() {
		table.Replace("s1", []challenges.Challenge{{Domain: "a.test", Token: "tok-1", KeyAuth: "ka-1"}})
		table.Replace("s2", []challenges.Challenge{{Domain: "b.test", Token: "tok-2", KeyAuth: "ka-2"}})

		keyAuth, found := table.KeyAuthByToken("tok-2")
		Expect(found).To(BeTrue())
		Expect(keyAuth).To(Equal("ka-2"))
	})

	It("forgets the entries a replace leaves out", func() {
		table.Replace("s1", []challenges.Challenge{{Domain: "a.test", Token: "tok-1", KeyAuth: "ka-1"}})
		table.Replace("s1", nil)

		keyAuth, found := table.KeyAuthByToken("tok-1")
		Expect(found).To(BeFalse())
		Expect(keyAuth).To(BeEmpty())
	})

	It("refuses a domain two secrets answer differently", func() {
		table.Replace("s1", []challenges.Challenge{{Domain: "A.test", Token: "tok-1", KeyAuth: "ka-1"}})
		table.Replace("s2", []challenges.Challenge{{Domain: "a.test", Token: "tok-2", KeyAuth: "ka-2"}})

		keyAuth, found := table.KeyAuthByDomain("a.test")
		Expect(found).To(BeFalse())
		Expect(keyAuth).To(BeEmpty())

		table.Replace("s2", []challenges.Challenge{{Domain: "a.test", Token: "tok-2", KeyAuth: "ka-1"}})

		keyAuth, found = table.KeyAuthByDomain("a.test")
		Expect(found).To(BeTrue())
		Expect(keyAuth).To(Equal("ka-1"))
	})

	Describe("WaitFor", func() {
		It("wakes a waiter on the replace holding its token", func() {
			done := make(chan error, 1)
			go func() {
				done <- table.WaitFor(context.Background(), "s1", "tok-1")
			}()

			Consistently(done, 50*time.Millisecond).ShouldNot(Receive())
			table.Replace("s1", []challenges.Challenge{{Domain: "a.test", Token: "tok-1", KeyAuth: "ka-1"}})

			Eventually(done).Should(Receive(BeNil()))
		})

		It("returns at once when the replace came before the call", func() {
			table.Replace("s1", []challenges.Challenge{{Domain: "a.test", Token: "tok-1", KeyAuth: "ka-1"}})

			Expect(table.WaitFor(context.Background(), "s1", "tok-1")).To(Succeed())
		})

		It("returns the context error when no replace holds the token", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			Expect(table.WaitFor(ctx, "s1", "tok-1")).To(MatchError(context.DeadlineExceeded))
			Expect(table.WaiterKeys()).To(BeZero())
		})

		It("keeps the key while another waiter of the token still waits", func() {
			shortCtx, cancelShort := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancelShort()
			done := make(chan error, 1)
			go func() {
				done <- table.WaitFor(context.Background(), "s1", "tok-1")
			}()
			Eventually(table.WaiterKeys).Should(Equal(1))

			Expect(table.WaitFor(shortCtx, "s1", "tok-1")).To(MatchError(context.DeadlineExceeded))
			Expect(table.WaiterKeys()).To(Equal(1))

			table.Replace("s1", []challenges.Challenge{{Domain: "a.test", Token: "tok-1", KeyAuth: "ka-1"}})
			Eventually(done).Should(Receive(BeNil()))
			Expect(table.WaiterKeys()).To(BeZero())
		})
	})
})
