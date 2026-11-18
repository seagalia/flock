package waiters_test

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/agentsdk/waiters"
)

var _ = Describe("SuspendWaiter", func() {
	var waiter *SuspendWaiter

	BeforeEach(func() {
		waiter = NewSuspendWaiter()
	})

	It("should not make wait when not suspended", func() {
		suspended, waitChan := waiter.ShouldWait()
		Expect(suspended).To(BeFalse())
		Expect(waitChan).To(BeNil())
	})

	It("should make wait while suspended and release on resume", func() {
		waiter.Suspend()
		suspended, waitChan := waiter.ShouldWait()
		Expect(suspended).To(BeTrue())
		Consistently(waitChan, "100ms").ShouldNot(Receive())

		waiter.Resume()
		Eventually(waitChan).Should(BeClosed())
		suspended, _ = waiter.ShouldWait()
		Expect(suspended).To(BeFalse())
	})

	It("should not block resume when a waiter never reads its channel", func() {
		waiter.Suspend()
		_, _ = waiter.ShouldWait()
		resumed := make(chan struct{})
		go func() {
			waiter.Resume()
			close(resumed)
		}()
		Eventually(resumed).Should(BeClosed())
	})

	It("should wait active operations to finish before being suspended", func() {
		waiter.AddActive()
		suspendDone := make(chan struct{})
		go func() {
			waiter.Suspend()
			close(suspendDone)
		}()
		Consistently(suspendDone, "200ms").ShouldNot(BeClosed())

		waiter.RemoveActive()
		Eventually(suspendDone, "1s").Should(BeClosed())
	})

	It("should release every waiter when suspend and resume race with waiters", func() {
		// meant for -race: a waiter asking during Resume must still be released
		wg := &sync.WaitGroup{}
		for range 50 {
			waiter.Suspend()
			wg.Go(func() {
				if suspended, waitChan := waiter.ShouldWait(); suspended {
					<-waitChan
				}
			})
			waiter.Resume()
		}
		allReleased := make(chan struct{})
		go func() {
			wg.Wait()
			close(allReleased)
		}()
		Eventually(allReleased, 2*time.Second).Should(BeClosed())
	})
})
