package metrics

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

type EphemeralStoreRegisterer struct {
	collectors map[string]prometheus.Collector
}

func NewEphemeralStoreRegisterer() *EphemeralStoreRegisterer {
	return &EphemeralStoreRegisterer{
		collectors: make(map[string]prometheus.Collector),
	}
}

func (n *EphemeralStoreRegisterer) Register(collector prometheus.Collector) error {
	n.collectors[fmt.Sprintf("%p", collector)] = collector
	return nil
}

func (n *EphemeralStoreRegisterer) MustRegister(collector ...prometheus.Collector) {
	for _, c := range collector {
		n.collectors[fmt.Sprintf("%p", c)] = c
	}
}

func (n *EphemeralStoreRegisterer) Unregister(collector prometheus.Collector) bool {
	delete(n.collectors, fmt.Sprintf("%p", collector))
	return true
}

func (n *EphemeralStoreRegisterer) List() []prometheus.Collector {
	collectors := make([]prometheus.Collector, 0, len(n.collectors))
	for _, c := range n.collectors {
		collectors = append(collectors, c)
	}
	return collectors
}
