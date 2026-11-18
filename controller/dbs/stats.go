package dbs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/controller/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{
	dbStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_controller",
		Subsystem: "dbstore",
		Name:      "db_status",
		Help:      "Current status of database connectivity, 0 is error",
	}, []string{
		"db_name",
	}),
}

type metrics struct {
	dbStatus *prometheus.GaugeVec
}

func init() {
	sdkmetrics.MustRegister(config.AppName, stats.dbStatus)
}

func (m *metrics) SetDbDown() {
	m.dbStatus.WithLabelValues("flock_db").Set(0)
}

func (m *metrics) SetDbUp() {
	m.dbStatus.WithLabelValues("flock_db").Set(1)
}
