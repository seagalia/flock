package app

import (
	"context"
	"net"
	"sync"

	"github.com/seagalia/flock/provisioners/acme/challenges"
)

func (a *App) RunCertManager(wg *sync.WaitGroup) {
	if a.certManager == nil {
		return
	}
	wg.Go(func() {
		a.certManager.Run(a.ctx)
	})
}

// RunChallengeServers answers the http-01 and tls-alpn-01 challenges from the table of the challenge secrets until the
// app ctx ends. A server failing is reported on hasError.
func (a *App) RunChallengeServers(wg *sync.WaitGroup, hasError chan<- struct{}) {
	run := func(name string, listener net.Listener, serve func(context.Context, net.Listener, *challenges.Table) error) {
		if listener == nil {
			return
		}
		wg.Go(func() {
			if err := serve(a.ctx, listener, a.challengeTable); err != nil {
				a.entry.Error().Err(err).Str("challenge", name).Msg("challenge server failed")
				reportFailure(hasError)
			}
		})
	}
	run("http-01", a.http01Listener, challenges.ServeHTTP01)
	run("tls-alpn-01", a.tlsAlpn01Listener, challenges.ServeTLSALPN01)
}
