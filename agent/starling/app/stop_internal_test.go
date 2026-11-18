package app

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/starling/bgp"
	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/sdk/agentsdk/aggregaters"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/logging"
)

// stopEvents records what the parts of a stopping app saw, in order
type stopEvents struct {
	mutex  sync.Mutex
	events []string
}

func (e *stopEvents) add(event string) {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.events = append(e.events, event)
}

func (e *stopEvents) list() []string {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return append([]string{}, e.events...)
}

// newStoppableApp holds what stop uses, in dev mode so the clean leaves the host alone
func newStoppableApp() *App {
	ctx, cancelFunc := context.WithCancel(context.Background())
	announceCtx, cancelAnnounce := context.WithCancel(ctx)
	rt := runtime.New()
	return &App{
		entry:          logging.Component("app"),
		cnf:            &config.Config{DevMode: true},
		rt:             rt,
		ctx:            ctx,
		cancelFunc:     cancelFunc,
		announceCtx:    announceCtx,
		cancelAnnounce: cancelAnnounce,
		aggregator:     aggregaters.NewAggregator("127.0.0.1:1", "instance", &core.Node{}, nil, rt.Waiter),
	}
}

var _ = Describe("App stop", func() {
	It("withdraws the announces before the app ctx ends", func() {
		app := newStoppableApp()
		events := &stopEvents{}
		announceWg := &sync.WaitGroup{}
		wg := &sync.WaitGroup{}
		announceWg.Go(func() {
			<-app.announceCtx.Done()
			// the BGP server waits the hold time of its peers before it is closed
			time.Sleep(100 * time.Millisecond)
			events.add("announce withdrawn")
		})
		wg.Go(func() {
			<-app.ctx.Done()
			events.add("app ctx cancelled")
		})

		Expect(app.stop(announceWg, wg, false)).To(Succeed())
		Expect(events.list()).To(Equal([]string{"announce withdrawn", "app ctx cancelled"}))
	})

	It("returns on the stop budget and doesn't panic when the handlers end later", func() {
		wg := &sync.WaitGroup{}
		release := make(chan struct{})
		wg.Go(func() { <-release })

		Expect(waitStopped(wg, 50*time.Millisecond)).To(MatchError(ContainSubstring("timeout")))
		close(release)
		wg.Wait()
		// the waiter of waitStopped runs after wg ends, a send on a channel already closed would panic there
		time.Sleep(100 * time.Millisecond)
	})
})

var _ = Describe("waitStopAsked", func() {
	DescribeTable("tells if the host must be cleaned when the ctx ends",
		func(cleanOnCtxDone, expected bool) {
			app := newStoppableApp()
			app.cleanOnCtxDone = cleanOnCtxDone
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			shouldClean, err := app.waitStopAsked(ctx, make(chan struct{}))

			Expect(err).ToNot(HaveOccurred())
			Expect(shouldClean).To(Equal(expected))
		},
		Entry("by default it leaves the host as is", false, false),
		Entry("after CleanOnCtxDone it cleans", true, true),
	)
})

var _ = Describe("RunBgpServer", func() {
	// runFailingBgp runs RunBgpServer with server and expects it to return and report the failure
	runFailingBgp := func(server *bgp.Server) {
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		app := &App{
			entry:       logging.Component("app"),
			cnf:         &config.Config{AdvertisementConfig: &config.AdvertisementConfig{BgpConfig: &config.BGPConfig{}}},
			ctx:         ctx,
			announceCtx: ctx,
			bgpServer:   server,
		}
		wg := &sync.WaitGroup{}
		hasError := make(chan struct{}, 1)

		returned := make(chan struct{})
		go func() {
			defer close(returned)
			app.RunBgpServer(wg, hasError)
		}()

		Eventually(returned, 2*time.Second).Should(BeClosed())
		Expect(hasError).To(Receive())
		wg.Wait()
	}

	It("returns and reports the failure of a BGP server failing before its init", func() {
		peers := []*config.BGPPeer{{Name: "peer", Address: "10.0.0.1", SrcInterface: "flock-missing0"}}
		// no router id, it is made from the source interface of the peer, which doesn't exist
		runFailingBgp(bgp.NewServer(65000, "", peers, nil))
	})

	It("returns and reports the failure of a BGP server failing to start", func() {
		// gobgp refuses a router id that isn't ipv4
		runFailingBgp(bgp.NewServer(65000, "::1", nil, nil))
	})
})
