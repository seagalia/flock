package l3logs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/agent/starling/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{
	incomingPackets: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "l3logs",
		Name:      "incoming_packets_total",
		Help:      "Number of incoming packets",
	}, []string{
		"source_ip",
		"destination_ip",
		"destination_port",
		"protocol",
		"ip_format",
		"vs_uuid",
	}),
	outgoingPackets: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "l3logs",
		Name:      "outgoing_packets_total",
		Help:      "Number of outgoing packets",
	}, []string{
		"source_ip",
		"destination_ip",
		"destination_port",
		"protocol",
		"ip_format",
		"vs_uuid",
	}),
}

type metrics struct {
	incomingPackets *prometheus.CounterVec
	outgoingPackets *prometheus.CounterVec
}

func init() {
	sdkmetrics.MustRegister(config.AppName, stats.incomingPackets)
	sdkmetrics.MustRegister(config.AppName, stats.outgoingPackets)
}
