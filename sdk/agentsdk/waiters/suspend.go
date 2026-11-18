package waiters

import (
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/logging"
	"sync"
	"sync/atomic"
	"time"
)

// SuspendWaiter holds the received resources of one agent app while its work is suspended. Its suspend metrics are
// shared by every waiter of the process because one /metrics serves them all, and only the starling agent suspends.
type SuspendWaiter struct {
	entry     zerolog.Logger
	suspended *atomic.Bool
	active    *atomic.Int32
	mutex     *sync.Mutex
	chanStore []chan struct{}
}

func NewSuspendWaiter() *SuspendWaiter {
	return &SuspendWaiter{
		entry:     logging.Component("suspend_waiter"),
		suspended: &atomic.Bool{},
		active:    &atomic.Int32{},
		mutex:     &sync.Mutex{},
		chanStore: []chan struct{}{},
	}
}

func (w *SuspendWaiter) Suspend() {
	w.entry.Info().Msg("Suspending all operations ...")
	w.suspended.Store(true)
	w.entry.Debug().Msg("Waiting for all active operations to finish ...")
	w.WaitActivated()
	w.entry.Debug().Msg("All active operations finished.")
	stats.AddSuspend()
	stats.IsSuspended()

}

func (w *SuspendWaiter) WaitActivated() {
	for {
		currentActive := w.active.Load()
		if currentActive < 0 {
			w.active.Store(0)
			currentActive = 0
		}
		if currentActive == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (w *SuspendWaiter) Resume() {
	w.entry.Info().Msg("Resuming all operations.")
	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.suspended.Store(false)
	for _, c := range w.chanStore {
		close(c)
	}
	w.chanStore = []chan struct{}{}
	stats.AddResume()
	stats.IsResumed()
}

func (w *SuspendWaiter) ShouldWait() (bool, chan struct{}) {
	if !w.suspended.Load() {
		return false, nil
	}
	w.mutex.Lock()
	defer w.mutex.Unlock()
	if !w.suspended.Load() {
		return false, nil
	}
	c := make(chan struct{})
	w.chanStore = append(w.chanStore, c)
	return true, c
}

func (w *SuspendWaiter) AddActive() {
	w.active.Add(1)
}

func (w *SuspendWaiter) RemoveActive() {
	w.active.Add(-1)
}
