package app

import (
	"sync"
)

// RunBgpServer returns once the BGP server runs, or once it failed, since Run returns before calling afterInit when
// it can't make its router id or start. The server stops with a.announceCtx, before the app ctx.
func (a *App) RunBgpServer(wg *sync.WaitGroup, hasError chan struct{}) {
	if a.cnf.AdvertisementConfig.BgpConfig == nil {
		return
	}
	initDone := make(chan struct{})
	runDone := make(chan struct{})
	a.entry.Info().Msg("Starting BGP server ...")
	wg.Go(func() {
		defer close(runDone)
		err := a.bgpServer.Run(a.announceCtx, func() { close(initDone) })
		if err != nil {
			a.entry.Error().Msgf("Bgp server error: %s", err)
			reportFailure(hasError)
		}
	})
	select {
	case <-initDone:
		a.entry.Info().Msg("BGP server started")
	case <-runDone:
	}
}
