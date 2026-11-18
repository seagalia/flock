package app

import (
	"github.com/seagalia/flock/sdk/logging"
	"reflect"
	"time"
)

func (a *App) startGarbage() {
	if a.cnf.WhenGarbageAfterStart == nil || *a.cnf.WhenGarbageAfterStart == 0 {
		return
	}
	entry := logging.Component("garbage")
	entry.Info().Msgf("garbage will be collected after %s", a.cnf.WhenGarbageAfterStart.String())
	go func() {
		ticker := time.NewTicker(time.Duration(*a.cnf.WhenGarbageAfterStart))
		defer ticker.Stop()
		select {
		case <-ticker.C:
		case <-a.ctx.Done():
			return
		}
		entry.Info().Msg("garbage collecting starting ...")
		a.rt.Waiter.Suspend()
		defer a.rt.Waiter.Resume()
		for _, gc := range a.garbageCollected {
			entryGc := entry.With().Stringer("type", reflect.TypeOf(gc)).Logger()
			entryGc.Debug().Msg("Starting garbage collecting component ...")
			err := gc.GarbageCollect()
			if err != nil {
				entryGc.Error().
					Err(err).
					Msg("garbage collect")
			}
			entryGc.Debug().Msg("Finished garbage collecting component.")
		}
		entry.Info().Msg("garbage collecting finished")
	}()
}
