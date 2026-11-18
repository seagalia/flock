// licensed to metallb authors under the apache license, version 2.0
// file: https://github.com/metallb/metallb/tree/main/internal/layer2/stats.go

package bgp

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/agent/starling/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{
	vipsSuccess: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "bgp",
		Name:      "number_vip_success",
		Help:      "Number of vip successfully added",
	}, []string{
		"vip",
	}),

	vipsFail: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "bgp",
		Name:      "number_vip_fail",
		Help:      "Number of vip failure when added",
	}, []string{
		"vip",
	}),

	peers: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "bgp",
		Name:      "peers",
		Help:      "1 when the bgp session with the peer is established, 0 otherwise",
	}, []string{
		"host",
		"name",
	}),

	announceInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "bgp",
		Name:      "announce",
		Help:      "Information about ip announced in bgp by this agent",
	}, []string{
		"ip",
	}),
}

type metrics struct {
	vipsSuccess  *prometheus.GaugeVec
	vipsFail     *prometheus.GaugeVec
	peers        *prometheus.GaugeVec
	announceInfo *prometheus.GaugeVec
}

func init() {
	sdkmetrics.MustRegister(config.AppName, stats.vipsSuccess)
	sdkmetrics.MustRegister(config.AppName, stats.vipsFail)
	sdkmetrics.MustRegister(config.AppName, stats.peers)
	sdkmetrics.MustRegister(config.AppName, stats.announceInfo)
}

func (m *metrics) SetPeerSession(peer *config.BGPPeer, established bool) {
	value := 0.0
	if established {
		value = 1
	}
	m.peers.WithLabelValues(peer.Address, peer.Name).Set(value)
}

func (m *metrics) AddAnnounced(ip string) {
	m.announceInfo.WithLabelValues(ip).Set(1)
}

func (m *metrics) RemoveAnnounced(ip string) {
	m.announceInfo.DeletePartialMatch(map[string]string{
		"ip": ip,
	})
}
