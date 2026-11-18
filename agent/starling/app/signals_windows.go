package app

import (
	"os"
)

// Windows has no user signals: nil never matches a received signal. The agent is
// not supported on windows, it only has to compile.
var (
	cleanSignal      os.Signal
	resetStartSignal os.Signal

	userSignals []os.Signal
)
