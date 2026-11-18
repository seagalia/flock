// licensed to metallb authors under the apache license, version 2.0
// file: https://github.com/metallb/metallb/tree/main/internal/layer2/stats.go

package layer2

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/agent/starling/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{
	in: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "layer2",
		Name:      "requests_received",
		Help:      "Number of layer2 requests received for owned IPs",
	}, []string{
		"ip",
	}),

	out: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "layer2",
		Name:      "responses_sent",
		Help:      "Number of layer2 responses sent for owned IPs in response to requests",
	}, []string{
		"ip",
	}),

	gratuitous: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "layer2",
		Name:      "gratuitous_sent",
		Help:      "Number of gratuitous layer2 packets sent for owned IPs as a result of failovers",
	}, []string{
		"ip",
	}),

	announceInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "layer2",
		Name:      "announce",
		Help:      "Information about ip announced in arp/ndp by this agent",
	}, []string{
		"ip",
	}),
}

type metrics struct {
	in           *prometheus.CounterVec
	out          *prometheus.CounterVec
	gratuitous   *prometheus.CounterVec
	announceInfo *prometheus.GaugeVec
}

func init() {
	sdkmetrics.MustRegister(config.AppName, stats.in)
	sdkmetrics.MustRegister(config.AppName, stats.out)
	sdkmetrics.MustRegister(config.AppName, stats.gratuitous)
	sdkmetrics.MustRegister(config.AppName, stats.announceInfo)
}

func (m *metrics) GotRequest(addr string) {
	m.in.WithLabelValues(addr).Add(1)
}

func (m *metrics) SentResponse(addr string) {
	m.out.WithLabelValues(addr).Add(1)
}

func (m *metrics) SentGratuitous(addr string) {
	m.gratuitous.WithLabelValues(addr).Add(1)
}

func (m *metrics) AddAnnounced(ip string) {
	m.announceInfo.WithLabelValues(ip).Set(1)
}

func (m *metrics) RemoveAnnounced(ip string) {
	m.announceInfo.DeletePartialMatch(map[string]string{
		"ip": ip,
	})
}
