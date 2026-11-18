package app

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/seagalia/flock/agent/starling/ipvsctl"
	"github.com/seagalia/flock/agent/starling/nm"
)

type machineIPsWatcher interface {
	Run(ctx context.Context) error
	Current(family int) []net.IP
	Changed() <-chan struct{}
}

// loadIPWatcher gives ipvs and the network manager the addresses of the machine the wildcard vips stand for. Dev
// mode fakes both, envoy serves a wildcard vip there with no address to follow.
func (a *App) loadIPWatcher() error {
	if a.cnf.DevMode {
		return nil
	}
	watcher, err := nm.NewIPWatcher(a.cnf.AdvertisementConfig.NetworkManager.DummyInterfaceName)
	if err != nil {
		return fmt.Errorf("follow the addresses of the machine: %w", err)
	}
	a.ipWatcher = watcher
	a.ipvsWildcards = ipvsctl.EnableWildcards(watcher)
	return nil
}

// RunIPWatcher follows the addresses of the machine on the wildcard vips until the app ctx ends
func (a *App) RunIPWatcher(wg *sync.WaitGroup, hasError chan struct{}) {
	if a.ipWatcher == nil {
		return
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		err := a.ipWatcher.Run(a.ctx)
		if err != nil {
			a.entry.Error().Err(err).Msg("failed to follow the addresses of the machine")
			reportFailure(hasError)
		}
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-a.ipWatcher.Changed():
			}
			a.syncWildcards()
		}
	}()
}

// syncWildcards logs what the retries of ipvs and the network manager left failed, the next change of the addresses
// tries again
func (a *App) syncWildcards() {
	if err := a.ipvsWildcards.Sync(a.ctx); err != nil && a.ctx.Err() == nil {
		a.entry.Error().Err(err).Msg("failed to follow the addresses of the machine on the wildcard ipvs services")
	}
	if err := a.netman.SyncWildcards(a.ctx); err != nil && a.ctx.Err() == nil {
		a.entry.Error().Err(err).Msg("failed to follow the addresses of the machine on the wildcard vip rules")
	}
}
