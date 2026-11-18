package app

import (
	"context"
	"sync"

	"github.com/seagalia/flock/agent/starling/waf/ctl"
)

func (a *App) RunWaf(wg *sync.WaitGroup, hasError chan struct{}) {
	wg.Add(1)
	runner := ctl.NewRunner(a.cnf.WafConfig, a.memory)
	a.isWafAlive = runner.IsAlive

	go func(ctx context.Context) {
		defer wg.Done()
		err := runner.Run(ctx)
		if err != nil {
			a.entry.Error().Msgf("Starling-waf run error: %s", err)
			reportFailure(hasError)
		}

	}(a.ctx)
}
