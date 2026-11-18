package measure

import (
	"context"
	"time"
)

// settledReads is how many reads in a row must stay within settledRatio of the first of them
const (
	settledReads = 3
	settledRatio = 0.10
)

// Settle reads every `every` until the heap in use stops moving, there is no endpoint to force a GC.
// After maxWait it gives the latest sample marked Unsettled: a phase that keeps growing is a finding, not a failure.
func Settle(ctx context.Context, read func(context.Context) (Sample, error), every, maxWait time.Duration) (Sample, error) {
	deadline := time.Now().Add(maxWait)
	window := make([]Sample, 0, settledReads)
	for {
		sample, err := read(ctx)
		if err != nil {
			return Sample{}, err
		}
		window = append(window, sample)
		if len(window) > settledReads {
			window = window[1:]
		}
		if len(window) == settledReads && isStable(window) {
			return sample, nil
		}
		if time.Now().After(deadline) {
			sample.Unsettled = true
			return sample, nil
		}
		select {
		case <-ctx.Done():
			return Sample{}, ctx.Err()
		case <-time.After(every):
		}
	}
}

func isStable(window []Sample) bool {
	first := float64(window[0].HeapInuse)
	for _, sample := range window[1:] {
		drift := float64(sample.HeapInuse) - first
		if drift < 0 {
			drift = -drift
		}
		if drift > first*settledRatio {
			return false
		}
	}
	return true
}
