package aggregaters

import (
	"github.com/prometheus/client_golang/prometheus"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{
	receive: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "aggregator",
		Name:      "receive",
		Help:      "Number of response received from controller for an object type",
	}, []string{
		"type_url",
	}),

	requestSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "aggregator",
		Name:      "request_success",
		Help:      "Number of request send in success to controller for an object type",
	}, []string{
		"type_url",
	}),

	requestFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "aggregator",
		Name:      "request_failure",
		Help:      "Number of request send in success to controller for an object type",
	}, []string{
		"type_url",
	}),

	connectSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "aggregator",
		Name:      "connect_success",
		Help:      "Number of connect success to controller for an object type",
	}, []string{
		"type_url",
	}),
	connectFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "aggregator",
		Name:      "connect_failure",
		Help:      "Number of connect failure to controller for an object type",
	}, []string{
		"type_url",
	}),
}

type metrics struct {
	receive        *prometheus.CounterVec
	requestSuccess *prometheus.CounterVec
	requestFailure *prometheus.CounterVec
	connectSuccess *prometheus.CounterVec
	connectFailure *prometheus.CounterVec
}

func init() {
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.receive)
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.requestSuccess)
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.requestFailure)
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.connectSuccess)
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.connectFailure)
}

func (m *metrics) AddReceive(typeUrl string) {
	m.receive.WithLabelValues(typeUrl).Add(1)
}

func (m *metrics) AddRequestSuccess(typeUrl string) {
	m.requestSuccess.WithLabelValues(typeUrl).Add(1)
}

func (m *metrics) AddRequestFailure(typeUrl string) {
	m.requestFailure.WithLabelValues(typeUrl).Add(1)
}

func (m *metrics) AddConnectSuccess(typeUrl string) {
	m.connectSuccess.WithLabelValues(typeUrl).Add(1)
}

func (m *metrics) AddConnectFailure(typeUrl string) {
	m.connectFailure.WithLabelValues(typeUrl).Add(1)
}
