// licensed to metallb authors under the apache license, version 2.0
// file: https://github.com/metallb/metallb/tree/main/internal/layer2/stats.go

package nm

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/agent/starling/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{
	vipIfaceSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "network_manager",
		Name:      "number_vip_iface_success",
		Help:      "Number of vips successfully added on the flock0 interface",
	}, []string{
		"ip_net",
	}),
	vipIfaceFail: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "network_manager",
		Name:      "number_vip_iface_fail",
		Help:      "Number of failures to add or delete a vip on the flock0 interface",
	}, []string{
		"ip_net",
	}),
}

type metrics struct {
	vipIfaceSuccess *prometheus.GaugeVec
	vipIfaceFail    *prometheus.GaugeVec
}

func init() {
	sdkmetrics.MustRegister(config.AppName, stats.vipIfaceSuccess)
	sdkmetrics.MustRegister(config.AppName, stats.vipIfaceFail)
}
