package agentlist

import (
	hmetrics "github.com/hashicorp/go-metrics"
	hprometheus "github.com/hashicorp/go-metrics/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/agent/starling/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
	"time"
)

var stats = metrics{
	nodeInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "agentlist",
		Name:      "info",
		Help:      "Information about agentlist instance",
	}, []string{
		"node_name",
		"node_host",
	}),
	nodeHealth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "agentlist",
		Name:      "member_status",
		Help:      "Member status of memberlist, 0 is disconnected and 1 is connected",
	}, []string{
		"node_host",
	}),
}

type metrics struct {
	nodeInfo   *prometheus.GaugeVec
	nodeHealth *prometheus.GaugeVec
}

func init() {
	sink, err := hprometheus.NewPrometheusSinkFrom(hprometheus.PrometheusOpts{
		Expiration: 60 * time.Second,
		Name:       "flock_agent_memberlist",
		Registerer: sdkmetrics.Registerer(config.AppName),
	})
	if err != nil {
		panic(err)
	}
	_, err = hmetrics.NewGlobal(&hmetrics.Config{
		ServiceName:          "flock_agent",
		HostName:             "",
		EnableHostname:       false,
		EnableRuntimeMetrics: false,
		EnableTypePrefix:     false,
		TimerGranularity:     time.Millisecond,
		ProfileInterval:      time.Second,
		FilterDefault:        true,
	}, sink)
	if err != nil {
		panic(err)
	}

	sdkmetrics.MustRegister(config.AppName, stats.nodeInfo)
	sdkmetrics.MustRegister(config.AppName, stats.nodeHealth)
}

func (m *metrics) SetNodeInfo(nodeName, host string) {
	m.nodeInfo.WithLabelValues(nodeName, host).Set(1)
}

func (m *metrics) Connected(member string) {
	m.nodeHealth.WithLabelValues(member).Set(1)
}

func (m *metrics) Disconnected(member string) {
	m.nodeHealth.WithLabelValues(member).Set(0)
}
