package backendctl

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/agent/starling/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{
	endpointState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "backendctl",
		Name:      "endpoint_state",
		Help:      "State of the endpoint associated with the backend, 0 is unhealthy, 1 is healthy",
	}, []string{
		"backend_uuid",
		"endpoint",
	}),

	backendState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "backendctl",
		Name:      "backend_state",
		Help:      "State of the backend, 0 is unhealthy, 1 is healthy",
	}, []string{
		"backend_uuid",
	}),
}

type metrics struct {
	endpointState *prometheus.GaugeVec
	backendState  *prometheus.GaugeVec
}

func init() {
	sdkmetrics.MustRegister(config.AppName, stats.endpointState)
	sdkmetrics.MustRegister(config.AppName, stats.backendState)
}

func (m *metrics) SetEndpointHealthy(backendUuid, endpoint string) {
	m.endpointState.WithLabelValues(backendUuid, endpoint).Set(1)
}

func (m *metrics) SetEndpointUnhealthy(backendUuid, endpoint string) {
	m.endpointState.WithLabelValues(backendUuid, endpoint).Set(0)
}

func (m *metrics) DeleteEndpoint(backendUuid, endpoint string) {
	m.endpointState.DeleteLabelValues(backendUuid, endpoint)
}

func (m *metrics) SetBackendHealthy(backendUuid string) {
	m.backendState.WithLabelValues(backendUuid).Set(1)
}

func (m *metrics) SetBackendUnhealthy(backendUuid string) {
	m.backendState.WithLabelValues(backendUuid).Set(0)
}

func (m *metrics) CleanForBackend(backendUuid string) {
	m.backendState.DeletePartialMatch(map[string]string{"backend_uuid": backendUuid})
	m.endpointState.DeletePartialMatch(map[string]string{"backend_uuid": backendUuid})
}
