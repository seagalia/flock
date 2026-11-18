package permissioners

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/controller/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{
	authFail: prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "authorization_failure",
			Help: "Number of authorization in error (permission check after login).",
		},
		[]string{"username", "resource_type", "resource_uuid"},
	),
	authSuccess: prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "authorization_success",
			Help: "Number of authorization in success (permission check after login).",
		},
		[]string{"username", "resource_type", "resource_uuid"},
	),
}

type metrics struct {
	authFail    *prometheus.CounterVec
	authSuccess *prometheus.CounterVec
}

func init() {
	sdkmetrics.MustRegister(config.AppName, stats.authSuccess)
	sdkmetrics.MustRegister(config.AppName, stats.authFail)
}
