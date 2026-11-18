package app

import "time"

// SetGracefulStopTimeout lets a spec check the fallback to Stop without waiting its real delay.
func SetGracefulStopTimeout(timeout time.Duration) (restore func()) {
	previous := gracefulStopTimeout
	gracefulStopTimeout = timeout
	return func() { gracefulStopTimeout = previous }
}
