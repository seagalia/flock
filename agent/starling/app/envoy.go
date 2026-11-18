package app

import (
	"context"
	"sync"

	"github.com/seagalia/flock/agent/starling/envoyctl"
)

func (a *App) RunEnvoy(wg *sync.WaitGroup, hasError chan struct{}) {
	wg.Add(1)

	logInJson := a.cnf.EnvoyConfig.LogInJson != nil && *a.cnf.EnvoyConfig.LogInJson
	if a.cnf.Log.InJson && a.cnf.EnvoyConfig.LogInJson == nil {
		logInJson = true
	}
	envoyctl.SetAccessLogStdoutInJson(logInJson)

	runner := envoyctl.NewRunner(a.cnf.EnvoyConfig, a.cnf.ServerConfig.ListenPort, a.cnf.WafConfig, a.memory)
	a.isEnvoyAlive = runner.IsEnvoyAlive
	go func(ctx context.Context) {
		defer wg.Done()
		err := runner.Run(ctx)
		if err != nil {
			a.entry.Error().Msgf("Envoy run error: %s", err)
			reportFailure(hasError)
		}

	}(a.ctx)
}

func (a *App) RunConfWatcherConsumer(wg *sync.WaitGroup, hasError chan struct{}) {
	wg.Add(1)
	a.isEnvoyAlive = func() error {
		return nil
	}
	consumer := envoyctl.NewDeltaWatchConsumer(a.configWatcher)
	go func(ctx context.Context) {
		defer wg.Done()
		err := consumer.Run(ctx)
		if err != nil {
			a.entry.Error().Msgf("Config watcher consumer run error: %s", err)
			reportFailure(hasError)
		}
	}(a.ctx)
}
