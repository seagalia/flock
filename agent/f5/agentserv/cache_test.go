package agentserv_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/f5/agentserv"
	"github.com/seagalia/flock/agent/f5/bigip"
)

type countingDevice struct {
	devicesCalls atomic.Int32
	poolCalls    atomic.Int32
	poolErr      error
	// release holds the reads until closed, nil reads at once
	release chan struct{}
}

func (c *countingDevice) Devices(context.Context) []bigip.DeviceHealth {
	c.devicesCalls.Add(1)
	if c.release != nil {
		<-c.release
	}
	return []bigip.DeviceHealth{{URL: "https://bigip-a", Reachable: true, Active: true}}
}

func (c *countingDevice) PoolMemberStates(_ context.Context, _, _, pool string) (map[string]bool, error) {
	c.poolCalls.Add(1)
	if c.release != nil {
		<-c.release
	}
	if c.poolErr != nil {
		return nil, c.poolErr
	}
	return map[string]bool{pool + ":80": true}, nil
}

var _ = Describe("CachedDevice", func() {
	var (
		dev *countingDevice
		ctx = context.Background()
	)

	BeforeEach(func() {
		dev = &countingDevice{}
	})

	It("reads the devices once per ttl", func() {
		cached := agentserv.NewCachedDevice(dev, time.Hour)

		Expect(cached.Devices(ctx)).To(HaveLen(1))
		Expect(cached.Devices(ctx)).To(HaveLen(1))

		Expect(dev.devicesCalls.Load()).To(Equal(int32(1)))
	})

	It("reads the devices again once the ttl passed", func() {
		cached := agentserv.NewCachedDevice(dev, 50*time.Millisecond)

		cached.Devices(ctx)
		time.Sleep(100 * time.Millisecond)
		cached.Devices(ctx)

		Expect(dev.devicesCalls.Load()).To(Equal(int32(2)))
	})

	It("keeps the member states of each pool apart", func() {
		cached := agentserv.NewCachedDevice(dev, time.Hour)

		statesA, err := cached.PoolMemberStates(ctx, "flock", "app", "pool_a")
		Expect(err).ToNot(HaveOccurred())
		statesB, err := cached.PoolMemberStates(ctx, "flock", "app", "pool_b")
		Expect(err).ToNot(HaveOccurred())
		_, err = cached.PoolMemberStates(ctx, "flock", "app", "pool_a")
		Expect(err).ToNot(HaveOccurred())

		Expect(statesA).To(HaveKey("pool_a:80"))
		Expect(statesB).To(HaveKey("pool_b:80"))
		Expect(dev.poolCalls.Load()).To(Equal(int32(2)))
	})

	It("doesn't keep a failed read", func() {
		dev.poolErr = errors.New("device busy")
		cached := agentserv.NewCachedDevice(dev, time.Hour)

		_, err := cached.PoolMemberStates(ctx, "flock", "app", "pool_a")
		Expect(err).To(MatchError("device busy"))
		dev.poolErr = nil
		states, err := cached.PoolMemberStates(ctx, "flock", "app", "pool_a")

		Expect(err).ToNot(HaveOccurred())
		Expect(states).To(HaveKey("pool_a:80"))
		Expect(dev.poolCalls.Load()).To(Equal(int32(2)))
	})

	It("reads once for concurrent callers", func() {
		dev.release = make(chan struct{})
		cached := agentserv.NewCachedDevice(dev, time.Hour)

		wg := sync.WaitGroup{}
		for range 5 {
			wg.Go(func() {
				Expect(cached.Devices(ctx)).To(HaveLen(1))
			})
		}
		Eventually(dev.devicesCalls.Load).Should(Equal(int32(1)))
		close(dev.release)
		wg.Wait()

		Expect(dev.devicesCalls.Load()).To(Equal(int32(1)))
	})

	It("returns the error of its caller's ctx while the shared read goes on", func() {
		dev.release = make(chan struct{})
		cached := agentserv.NewCachedDevice(dev, time.Hour)
		callerCtx, cancel := context.WithCancel(ctx)
		errs := make(chan error, 1)
		go func() {
			_, err := cached.PoolMemberStates(callerCtx, "flock", "app", "pool_a")
			errs <- err
		}()
		Eventually(dev.poolCalls.Load).Should(Equal(int32(1)))

		cancel()

		Eventually(errs).Should(Receive(MatchError(context.Canceled)))
		close(dev.release)
		states, err := cached.PoolMemberStates(ctx, "flock", "app", "pool_a")
		Expect(err).ToNot(HaveOccurred())
		Expect(states).To(HaveKey("pool_a:80"))
		Expect(dev.poolCalls.Load()).To(Equal(int32(1)))
	})

	It("gives no device to a caller gone while the shared read goes on", func() {
		dev.release = make(chan struct{})
		cached := agentserv.NewCachedDevice(dev, time.Hour)
		callerCtx, cancel := context.WithCancel(ctx)
		devices := make(chan []bigip.DeviceHealth, 1)
		go func() {
			devices <- cached.Devices(callerCtx)
		}()
		Eventually(dev.devicesCalls.Load).Should(Equal(int32(1)))

		cancel()

		Eventually(devices).Should(Receive(BeNil()))
		close(dev.release)
		Expect(cached.Devices(ctx)).To(HaveLen(1))
		Expect(dev.devicesCalls.Load()).To(Equal(int32(1)))
	})
})
