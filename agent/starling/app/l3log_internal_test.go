package app

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"

	"github.com/seagalia/flock/agent/starling/config"
)

type fakeL3Logger struct {
	run func(ctx context.Context, afterInit func()) error
}

func (f fakeL3Logger) Run(ctx context.Context, afterInit func()) error {
	return f.run(ctx, afterInit)
}

var _ = Describe("runL3Logger", func() {
	var (
		app      *App
		cancel   context.CancelFunc
		wg       *sync.WaitGroup
		hasError chan struct{}
	)

	BeforeEach(func() {
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		app = &App{cnf: &config.Config{}, ctx: ctx, entry: zerolog.Nop()}
		wg = &sync.WaitGroup{}
		hasError = make(chan struct{}, 1)
	})

	AfterEach(func() {
		cancel()
		wg.Wait()
	})

	It("returns once the logger collects", func() {
		logger := fakeL3Logger{run: func(ctx context.Context, afterInit func()) error {
			afterInit()
			<-ctx.Done()
			return nil
		}}

		app.runL3Logger(logger, wg, hasError)

		Consistently(hasError).ShouldNot(Receive())
	})

	It("returns and reports the error when the logger fails before its init", func() {
		logger := fakeL3Logger{run: func(context.Context, func()) error {
			return errors.New("netlink receive: operation not permitted")
		}}

		done := make(chan struct{})
		go func() {
			app.runL3Logger(logger, wg, hasError)
			close(done)
		}()

		Eventually(done).Should(BeClosed())
		Eventually(hasError).Should(Receive())
	})
})

var _ = Describe("RunL3Log", func() {
	It("collects nothing in dev mode", func() {
		app := &App{cnf: &config.Config{DevMode: true}, ctx: context.Background(), entry: zerolog.Nop()}
		wg := &sync.WaitGroup{}

		app.RunL3Log(wg, make(chan struct{}, 1))

		wg.Wait()
	})
})
