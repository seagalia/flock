package measure

import (
	"fmt"
	"strings"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// Sample is the memory of one flock process at one moment, in bytes
type Sample struct {
	HeapInuse  uint64
	HeapAlloc  uint64
	StackInuse uint64 // goroutine stacks, outside the heap
	Sys        uint64
	RSS        uint64
	Goroutines int
	// ContainerMemory is what docker stats shows for the whole container, filled by the suite
	ContainerMemory uint64
	// Unsettled is set when the heap was still moving at the end of the wait
	Unsettled bool
}

// ParseSample reads a sample from a Prometheus text exposition
func ParseSample(metricsText string) (Sample, error) {
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(metricsText))
	if err != nil {
		return Sample{}, fmt.Errorf("parse metrics: %w", err)
	}
	value := func(name string) (float64, error) {
		family, ok := families[name]
		if !ok || len(family.GetMetric()) == 0 {
			return 0, fmt.Errorf("metric %s not found", name)
		}
		return family.GetMetric()[0].GetGauge().GetValue(), nil
	}

	var heapInuse, heapAlloc, stackInuse, sys, rss, goroutines float64
	wanted := []struct {
		name string
		into *float64
	}{
		{"go_memstats_heap_inuse_bytes", &heapInuse},
		{"go_memstats_heap_alloc_bytes", &heapAlloc},
		{"go_memstats_stack_inuse_bytes", &stackInuse},
		{"go_memstats_sys_bytes", &sys},
		{"process_resident_memory_bytes", &rss},
		{"go_goroutines", &goroutines},
	}
	for _, metric := range wanted {
		*metric.into, err = value(metric.name)
		if err != nil {
			return Sample{}, err
		}
	}
	return Sample{
		HeapInuse:  uint64(heapInuse),
		HeapAlloc:  uint64(heapAlloc),
		StackInuse: uint64(stackInuse),
		Sys:        uint64(sys),
		RSS:        uint64(rss),
		Goroutines: int(goroutines),
	}, nil
}
