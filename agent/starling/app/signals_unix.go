//go:build !windows

package app

import (
	"os"
	"syscall"
)

var (
	cleanSignal      os.Signal = syscall.SIGUSR1
	resetStartSignal os.Signal = syscall.SIGUSR2

	// the root ctx of the process ends on SIGINT and SIGTERM
	userSignals = []os.Signal{cleanSignal, resetStartSignal}
)
