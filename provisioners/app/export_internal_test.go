package app

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
)

// setDrainGrace shortens the drain grace for one spec
func setDrainGrace(grace time.Duration) {
	previous := drainGrace
	drainGrace = grace
	DeferCleanup(func() { drainGrace = previous })
}
