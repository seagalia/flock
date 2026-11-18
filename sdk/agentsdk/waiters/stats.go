package waiters

import (
	"github.com/prometheus/client_golang/prometheus"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{
	suspend: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "waiters",
		Name:      "suspend",
		Help:      "Number of suspend received",
	}, []string{}),

	resume: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "waiters",
		Name:      "resume",
		Help:      "Number of resume received",
	}, []string{}),

	state: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "waiters",
		Name:      "state",
		Help:      "State of the waiters, 0 if not suspended, 1 if suspended",
	}, []string{}),
}

type metrics struct {
	suspend *prometheus.CounterVec
	resume  *prometheus.CounterVec
	state   *prometheus.GaugeVec
}

func init() {
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.suspend)
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.resume)
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.state)
	stats.IsResumed()
}

func (m *metrics) IsSuspended() {
	m.state.With(prometheus.Labels{}).Set(1)
}

func (m *metrics) IsResumed() {
	m.state.With(prometheus.Labels{}).Set(0)
}

func (m *metrics) AddSuspend() {
	m.suspend.With(prometheus.Labels{}).Add(1)
}

func (m *metrics) AddResume() {
	m.resume.With(prometheus.Labels{}).Add(1)
}
