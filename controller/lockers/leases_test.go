package lockers_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/controller/lockers"
)

var _ = Describe("Leases", func() {
	const subject = "acme"
	const ttl = 30 * time.Second

	var (
		leases *lockers.Leases
		first  = lockers.LeaseHolder{Uuid: "uuid-1", Name: "prov-1"}
		second = lockers.LeaseHolder{Uuid: "uuid-2", Name: "prov-2"}
		ctx    = context.Background()
	)

	BeforeEach(func() {
		var err error
		leases, err = lockers.NewLeases(ctx, dbMan)
		Expect(err).ToNot(HaveOccurred())
		_, err = dbMan.Exec(ctx, dbs.MakeDbQuery("DELETE FROM "+lockers.TableLeases, nil))
		Expect(err).ToNot(HaveOccurred())
	})

	It("grants a free subject", func() {
		lease, err := leases.Acquire(ctx, subject, first, ttl)
		Expect(err).ToNot(HaveOccurred())
		Expect(lease.Granted).To(BeTrue())
		Expect(lease.Id).To(HaveLen(36))
		Expect(lease.Holder).To(Equal(first))
		Expect(lease.ExpiresAt).To(BeTemporally("~", time.Now().Add(ttl), time.Second))
	})

	It("refuses a held subject with its holder", func() {
		granted, err := leases.Acquire(ctx, subject, first, ttl)
		Expect(err).ToNot(HaveOccurred())

		refused, err := leases.Acquire(ctx, subject, second, ttl)
		Expect(err).ToNot(HaveOccurred())
		Expect(refused.Granted).To(BeFalse())
		Expect(refused.Holder).To(Equal(first))
		Expect(refused.Id).To(BeEmpty())
		Expect(refused.ExpiresAt).To(BeTemporally("==", granted.ExpiresAt))
	})

	It("takes over an expired lease", func() {
		granted, err := leases.Acquire(ctx, subject, first, 50*time.Millisecond)
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() lockers.Lease {
			lease, err := leases.Acquire(ctx, subject, second, ttl)
			Expect(err).ToNot(HaveOccurred())
			return lease
		}).Should(And(
			HaveField("Granted", true),
			HaveField("Holder", second),
			HaveField("Id", Not(Equal(granted.Id))),
		))
	})

	It("grants a subject to one of the holders acquiring it at once", func() {
		const holders = 8
		results := make([]lockers.Lease, holders)
		errs := make([]error, holders)
		var acquiring sync.WaitGroup
		for i := range holders {
			holder := lockers.LeaseHolder{Uuid: "uuid", Name: fmt.Sprintf("prov-%d", i)}
			acquiring.Go(func() { results[i], errs[i] = leases.Acquire(ctx, subject, holder, ttl) })
		}
		acquiring.Wait()

		var granted []lockers.Lease
		for i := range holders {
			Expect(errs[i]).ToNot(HaveOccurred())
			if results[i].Granted {
				granted = append(granted, results[i])
			}
		}
		Expect(granted).To(HaveLen(1))
		for _, lease := range results {
			Expect(lease.Holder).To(Equal(granted[0].Holder))
		}
	})

	It("keeps a fresh lease when its holder acquires again", func() {
		granted, err := leases.Acquire(ctx, subject, first, ttl)
		Expect(err).ToNot(HaveOccurred())

		again, err := leases.Acquire(ctx, subject, first, ttl)
		Expect(err).ToNot(HaveOccurred())
		Expect(again.Granted).To(BeFalse())
		Expect(again.Holder).To(Equal(first))
		Expect(leases.Renew(ctx, granted.Id, first.Name, ttl)).Error().ToNot(HaveOccurred())
	})

	It("renews only for the holder and the lease id", func() {
		granted, err := leases.Acquire(ctx, subject, first, ttl)
		Expect(err).ToNot(HaveOccurred())

		time.Sleep(10 * time.Millisecond)
		expiresAt, err := leases.Renew(ctx, granted.Id, first.Name, ttl)
		Expect(err).ToNot(HaveOccurred())
		Expect(expiresAt).To(BeTemporally(">", granted.ExpiresAt))

		_, err = leases.Renew(ctx, granted.Id, second.Name, ttl)
		Expect(err).To(MatchError(lockers.ErrLeaseLost))
		_, err = leases.Renew(ctx, "other-id", first.Name, ttl)
		Expect(err).To(MatchError(lockers.ErrLeaseLost))
	})

	It("answers lost on the renewal of an expired lease", func() {
		granted, err := leases.Acquire(ctx, subject, first, 50*time.Millisecond)
		Expect(err).ToNot(HaveOccurred())

		time.Sleep(100 * time.Millisecond)
		_, err = leases.Renew(ctx, granted.Id, first.Name, ttl)
		Expect(err).To(MatchError(lockers.ErrLeaseLost))
	})

	It("releases idempotently", func() {
		granted, err := leases.Acquire(ctx, subject, first, ttl)
		Expect(err).ToNot(HaveOccurred())

		Expect(leases.Release(ctx, granted.Id, second.Name)).To(Succeed())
		held, err := leases.Acquire(ctx, subject, second, ttl)
		Expect(err).ToNot(HaveOccurred())
		Expect(held.Granted).To(BeFalse())

		Expect(leases.Release(ctx, granted.Id, first.Name)).To(Succeed())
		Expect(leases.Release(ctx, granted.Id, first.Name)).To(Succeed())
		next, err := leases.Acquire(ctx, subject, second, ttl)
		Expect(err).ToNot(HaveOccurred())
		Expect(next.Granted).To(BeTrue())
	})

	It("rolls back an acquire cut by its ctx", func() {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := leases.Acquire(cancelled, subject, first, ttl)
		Expect(err).To(MatchError(context.Canceled))

		lease, err := leases.Acquire(ctx, subject, second, ttl)
		Expect(err).ToNot(HaveOccurred())
		Expect(lease.Granted).To(BeTrue())
	})
})
