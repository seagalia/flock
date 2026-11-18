// licensed to metallb authors under the apache license, version 2.0
// file: https://github.com/metallb/metallb/tree/main/internal/layer2/stats.go

package envoyctl

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/agent/starling/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{
	envoyResInError: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "envoyctl",
		Name:      "envoy_resources_in_error",
		Help:      "Number of envoy resources in error",
	}, []string{
		"envoy_resource_type",
	}),
}

type metrics struct {
	envoyResInError *prometheus.GaugeVec
}

func init() {
	sdkmetrics.MustRegister(config.AppName, stats.envoyResInError)
}
