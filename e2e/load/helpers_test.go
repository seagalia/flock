//go:build load

package load_test

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"

	"github.com/seagalia/flock/e2e/load/measure"
)

func knob(name string, def int) int {
	GinkgoHelper()
	value, err := measure.Knob(name, def)
	Expect(err).NotTo(HaveOccurred())
	return value
}

func duration(name string, def time.Duration) time.Duration {
	GinkgoHelper()
	value, err := measure.Duration(name, def)
	Expect(err).NotTo(HaveOccurred())
	return value
}

// measured is a flock process to sample: its metrics without anything proxied, and the container it runs in
type measured struct {
	metrics   func(ctx context.Context) (string, error)
	container testcontainers.Container
}

// settled waits for the heap to stop moving, for phases measured at rest
func (m measured) settled(ctx context.Context) measure.Sample {
	GinkgoHelper()
	sample, err := measure.Settle(ctx, m.read, 5*time.Second, 2*time.Minute)
	Expect(err).NotTo(HaveOccurred())
	return sample
}

// now reads once, for phases measured while the load still runs
func (m measured) now(ctx context.Context) measure.Sample {
	GinkgoHelper()
	sample, err := m.read(ctx)
	Expect(err).NotTo(HaveOccurred())
	return sample
}

func (m measured) read(ctx context.Context) (measure.Sample, error) {
	text, err := m.metrics(ctx)
	if err != nil {
		return measure.Sample{}, err
	}
	sample, err := measure.ParseSample(text)
	if err != nil {
		return measure.Sample{}, err
	}
	sample.ContainerMemory, err = containerMemory(ctx, m.container)
	return sample, err
}

// containerMemory is what docker stats shows: the usage without the inactive page cache
func containerMemory(ctx context.Context, c testcontainers.Container) (uint64, error) {
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return 0, err
	}
	defer provider.Close()
	result, err := provider.Client().ContainerStats(ctx, c.GetContainerID(), client.ContainerStatsOptions{})
	if err != nil {
		return 0, err
	}
	defer result.Body.Close()
	var stats container.StatsResponse
	err = json.NewDecoder(result.Body).Decode(&stats)
	if err != nil {
		return 0, err
	}
	memory := stats.MemoryStats
	// cgroup v2 names it inactive_file, cgroup v1 total_inactive_file
	inactive := memory.Stats["inactive_file"] + memory.Stats["total_inactive_file"]
	return memory.Usage - min(inactive, memory.Usage), nil
}

// parallel runs fn for 0..n-1 on workers goroutines, the api calls of a seeding would take long one by one
func parallel(n, workers int, fn func(i int)) {
	GinkgoHelper()
	indexes := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			// recovered per call: a worker that died on a failure would leave the feeding loop blocked
			for i := range indexes {
				func() {
					defer GinkgoRecover()
					fn(i)
				}()
			}
		})
	}
	for i := range n {
		indexes <- i
	}
	close(indexes)
	wg.Wait()
}
