package auth

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/controller/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{
	loginFailure: prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "login_failure",
			Help: "Number of login in failure.",
		},
		[]string{"username"},
	),
	loginSuccess: prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "login_success",
			Help: "Number of login in success.",
		},
		[]string{"username"},
	),
	loginLockedOut: prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "login_locked_out",
			Help: "Login which are in locked-out state, 1 means locked.",
		},
		[]string{"username"},
	),
}

type metrics struct {
	loginFailure   *prometheus.CounterVec
	loginSuccess   *prometheus.CounterVec
	loginLockedOut *prometheus.GaugeVec
}

func init() {
	sdkmetrics.MustRegister(config.AppName, stats.loginFailure)
	sdkmetrics.MustRegister(config.AppName, stats.loginSuccess)
	sdkmetrics.MustRegister(config.AppName, stats.loginLockedOut)
}
