package reconciler

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/agent/f5/config"
	"github.com/seagalia/flock/sdk/metrics"
)

var (
	declareTotal = metrics.NewCounterVec(config.AppName, prometheus.CounterOpts{
		Name: "flock_f5_agent_declare_total",
		Help: "AS3 declarations posted, by tenant and result (success, refused, retry).",
	}, []string{"tenant", "result"})
	declareDuration = metrics.NewHistogram(config.AppName, prometheus.HistogramOpts{
		Name:    "flock_f5_agent_declare_duration_seconds",
		Help:    "Time an AS3 declaration takes, its task included.",
		Buckets: []float64{0.5, 1, 2, 5, 10, 20, 40, 80, 120},
	})
	retriesTotal = metrics.NewCounter(config.AppName, prometheus.CounterOpts{
		Name: "flock_f5_agent_retries_total",
		Help: "Passes scheduled again after a retryable failure of the device.",
	})
	tenantsGauge = metrics.NewGauge(config.AppName, prometheus.GaugeOpts{
		Name: "flock_f5_agent_tenants",
		Help: "AS3 tenants the agent posted.",
	})
	driftCorrections = metrics.NewCounter(config.AppName, prometheus.CounterOpts{
		Name: "flock_f5_agent_drift_corrections_total",
		Help: "Resyncs that found a tenant changed on the device and declared it again.",
	})
)
