package lockers_test

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/controller/lockers"
)

// lockWithin tells whether locker gets subject before timeout.
func lockWithin(locker lockers.Locker, subject string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return locker.Lock(ctx, subject) == nil
}

var _ = Describe("Memory", func() {
	It("should let one holder of a subject at a time", func() {
		locker := lockers.NewMemory()
		Expect(locker.Lock(context.Background(), lockers.ZonePools)).To(Succeed())

		locked := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			Expect(locker.Lock(context.Background(), lockers.ZonePools)).To(Succeed())
			close(locked)
		}()
		Consistently(locked, 100*time.Millisecond).ShouldNot(BeClosed())

		locker.Unlock(context.Background(), lockers.ZonePools)
		Eventually(locked).Should(BeClosed())
	})
	It("should give up when the context is done", func() {
		locker := lockers.NewMemory()
		Expect(locker.Lock(context.Background(), lockers.ZonePools)).To(Succeed())

		Expect(lockWithin(locker, lockers.ZonePools, 100*time.Millisecond)).To(BeFalse())
	})
	It("should lock subjects independently", func() {
		locker := lockers.NewMemory()
		Expect(locker.Lock(context.Background(), lockers.ZonePools)).To(Succeed())

		Expect(lockWithin(locker, lockers.InstanceVips, time.Second)).To(BeTrue())
	})
})

// one db manager for the suite: it registers prometheus collectors globally
var dbMan *dbs.DbManager

var _ = BeforeSuite(func() {
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	var err error
	dbMan, err = dbs.NewDbManager(ctx, dbs.DbTypeSqlite, filepath.Join(GinkgoT().TempDir(), "locks.db"), time.Hour, time.Hour, 1, 1)
	Expect(err).ToNot(HaveOccurred())
})

var _ = Describe("Db", func() {
	// two controllers sharing the database
	var controllerA, controllerB *lockers.Db

	newLocker := func(ttl time.Duration) *lockers.Db {
		locker, err := lockers.NewDb(context.Background(), dbMan, ttl)
		Expect(err).ToNot(HaveOccurred())
		return locker
	}
	lockRows := func() int {
		var count int
		Expect(dbMan.QueryRow(context.Background(), dbs.MakeDbQuery("SELECT COUNT(*) FROM "+lockers.TableLocks, nil)).Scan(&count)).To(Succeed())
		return count
	}

	BeforeEach(func() {
		controllerA = newLocker(lockers.DefaultTTL)
		_, err := dbMan.Exec(context.Background(), dbs.MakeDbQuery("DELETE FROM "+lockers.TableLocks, nil))
		Expect(err).ToNot(HaveOccurred())
		controllerB = newLocker(lockers.DefaultTTL)
	})

	It("should make another controller wait until the subject is unlocked", func() {
		Expect(controllerA.Lock(context.Background(), lockers.ZonePools)).To(Succeed())

		locked := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			Expect(controllerB.Lock(context.Background(), lockers.ZonePools)).To(Succeed())
			close(locked)
		}()
		Consistently(locked, 300*time.Millisecond).ShouldNot(BeClosed())

		controllerA.Unlock(context.Background(), lockers.ZonePools)
		Eventually(locked).Should(BeClosed())
		controllerB.Unlock(context.Background(), lockers.ZonePools)
	})
	It("should let one goroutine of a controller hold a subject at a time", func() {
		Expect(controllerA.Lock(context.Background(), lockers.ZonePools)).To(Succeed())
		DeferCleanup(controllerA.Unlock, context.Background(), lockers.ZonePools)

		Expect(lockWithin(controllerA, lockers.ZonePools, 200*time.Millisecond)).To(BeFalse())
	})
	It("should give up when the context is done and keep the subject lockable", func() {
		Expect(controllerA.Lock(context.Background(), lockers.ZonePools)).To(Succeed())

		Expect(lockWithin(controllerB, lockers.ZonePools, 200*time.Millisecond)).To(BeFalse())

		controllerA.Unlock(context.Background(), lockers.ZonePools)
		Expect(lockWithin(controllerB, lockers.ZonePools, time.Second)).To(BeTrue())
		controllerB.Unlock(context.Background(), lockers.ZonePools)
	})
	It("should lock subjects independently", func() {
		Expect(controllerA.Lock(context.Background(), lockers.ZonePools)).To(Succeed())
		DeferCleanup(controllerA.Unlock, context.Background(), lockers.ZonePools)

		Expect(lockWithin(controllerB, lockers.InstanceVips, time.Second)).To(BeTrue())
		controllerB.Unlock(context.Background(), lockers.InstanceVips)
	})
	It("should delete the row of the subject on unlock", func() {
		Expect(controllerA.Lock(context.Background(), lockers.ZonePools)).To(Succeed())
		Expect(lockRows()).To(Equal(1))

		controllerA.Unlock(context.Background(), lockers.ZonePools)

		Expect(lockRows()).To(BeZero())
	})
	It("should delete the row on unlock even when the caller's ctx is cancelled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		Expect(controllerA.Lock(ctx, lockers.ZonePools)).To(Succeed())
		cancel()

		controllerA.Unlock(ctx, lockers.ZonePools)

		Expect(lockRows()).To(BeZero())
		Expect(lockWithin(controllerB, lockers.ZonePools, time.Second)).To(BeTrue())
		controllerB.Unlock(context.Background(), lockers.ZonePools)
	})
	It("should keep renewing after the ctx of Lock is cancelled", func() {
		controllerA = newLocker(300 * time.Millisecond)
		ctx, cancel := context.WithCancel(context.Background())
		Expect(controllerA.Lock(ctx, lockers.ZonePools)).To(Succeed())
		DeferCleanup(controllerA.Unlock, context.Background(), lockers.ZonePools)
		cancel()
		time.Sleep(time.Second)

		Expect(lockWithin(controllerB, lockers.ZonePools, 300*time.Millisecond)).To(BeFalse())
	})
	It("should take over the expired lock of a crashed controller", func() {
		_, err := dbMan.Exec(context.Background(), dbs.MakeDbQuery(
			"INSERT INTO "+lockers.TableLocks+" (subject, owner, expires_at) VALUES (?, ?, ?)",
			[]any{lockers.ZonePools, "crashed-controller", 0},
		))
		Expect(err).ToNot(HaveOccurred())

		Expect(lockWithin(controllerA, lockers.ZonePools, time.Second)).To(BeTrue())
		controllerA.Unlock(context.Background(), lockers.ZonePools)
	})
	It("should give a subject to one of the controllers locking it at once", func() {
		const controllers = 8
		var holders atomic.Int32
		var locking sync.WaitGroup
		for range controllers {
			locker := newLocker(lockers.DefaultTTL)
			locking.Go(func() {
				if lockWithin(locker, lockers.ZonePools, 300*time.Millisecond) {
					holders.Add(1)
					DeferCleanup(locker.Unlock, context.Background(), lockers.ZonePools)
				}
			})
		}
		locking.Wait()

		Expect(holders.Load()).To(BeEquivalentTo(1))
		Expect(lockRows()).To(Equal(1))
	})
	It("should keep the lock of another controller until it expires", func() {
		_, err := dbMan.Exec(context.Background(), dbs.MakeDbQuery(
			"INSERT INTO "+lockers.TableLocks+" (subject, owner, expires_at) VALUES (?, ?, ?)",
			[]any{lockers.ZonePools, "other-controller", time.Now().Add(time.Hour).UnixMilli()},
		))
		Expect(err).ToNot(HaveOccurred())

		Expect(lockWithin(controllerA, lockers.ZonePools, 200*time.Millisecond)).To(BeFalse())
	})
	It("should keep a lock held longer than its ttl", func() {
		controllerA = newLocker(300 * time.Millisecond)
		Expect(controllerA.Lock(context.Background(), lockers.ZonePools)).To(Succeed())
		DeferCleanup(controllerA.Unlock, context.Background(), lockers.ZonePools)
		time.Sleep(time.Second)

		Expect(lockWithin(controllerB, lockers.ZonePools, 300*time.Millisecond)).To(BeFalse())
	})
})
