package app

import (
	"sync"

	"github.com/seagalia/flock/agent/starling/l3logs"
)

func (a *App) RunL3Log(wg *sync.WaitGroup, hasError chan struct{}) {
	// dev mode installs no nftables rule, so nothing would reach the nflog group, and reading it needs rights a user
	// running flock dev on linux doesn't have
	if a.cnf.DisableL3Log || a.cnf.DevMode {
		return
	}
	a.runL3Logger(l3logs.NewL3Logger(a.rt.IpPorts), wg, hasError)
}

// runL3Logger returns once the logger collects, or once it failed: Run returns before calling afterInit when it can't
// open or register the nflog socket.
func (a *App) runL3Logger(l3log l3logs.L3Logger, wg *sync.WaitGroup, hasError chan struct{}) {
	initDone := make(chan struct{})
	runDone := make(chan struct{})
	wg.Add(1)
	a.entry.Info().Msg("Starting L3 Log collect ...")
	go func() {
		defer wg.Done()
		defer close(runDone)
		err := l3log.Run(a.ctx, func() { close(initDone) })
		if err != nil {
			a.entry.Error().Msgf("l3logger error: %s", err)
			reportFailure(hasError)
		}
	}()
	select {
	case <-initDone:
		a.entry.Info().Msg("L3 log collecting")
	case <-runDone:
	}
}
