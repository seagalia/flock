package manager

import (
	"context"
	"errors"
	"time"

	"github.com/moby/moby/api/types/events"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/provisioners/docker/eds"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
)

var _ = Describe("Run", func() {
	var (
		docker   *fakeDocker
		cancel   context.CancelFunc
		runEnded chan error
	)

	BeforeEach(func() {
		quietDelay = 50 * time.Millisecond
		maxDelay = 300 * time.Millisecond
		resyncInterval = time.Hour
		minReconnectWait = 10 * time.Millisecond
		DeferCleanup(func() {
			quietDelay = defaultQuietDelay
			maxDelay = defaultMaxDelay
			resyncInterval = defaultResyncInterval
			minReconnectWait = defaultMinReconnectWait
		})

		docker = &fakeDocker{}
		docker.setContainers(composeProject())
		controller := newFakeController()
		controller.vips.put(&vip.VIP{Name: "local", Ip: "0.0.0.0"})
		manager := NewManager(testConfig(), docker, eds.NewServer(), controller.clients(), fakeIdentity{})

		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		runEnded = make(chan error, 1)
		go func() { runEnded <- manager.Run(ctx) }()
		DeferCleanup(func() {
			cancel()
			Eventually(runEnded).Should(Receive(BeNil()))
		})
		Eventually(docker.listCount).Should(Equal(1))
	})

	send := func() {
		GinkgoHelper()
		messages, _ := docker.channels()
		Eventually(messages).Should(BeSent(events.Message{Type: events.ContainerEventType}))
	}

	It("rebuilds once after a burst of events quieted down", func() {
		for range 5 {
			send()
		}
		Eventually(docker.listCount).Should(Equal(2))
		Consistently(docker.listCount, 3*quietDelay).Should(Equal(2))
	})

	It("rebuilds every max delay while the events never stop", func() {
		start := time.Now()
		for time.Since(start) < 2*maxDelay {
			send()
			time.Sleep(quietDelay / 2)
		}
		Expect(docker.listCount()).To(BeNumerically(">=", 2))
	})

	It("subscribes again and rebuilds after the events stream failed", func() {
		_, errs := docker.channels()
		errs <- errors.New("daemon restarted")

		Eventually(docker.subscriptionCount).Should(Equal(2))
		Eventually(docker.listCount).Should(BeNumerically(">=", 2))
	})
})
