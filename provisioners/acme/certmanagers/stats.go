package certmanagers

import (
	"github.com/seagalia/flock/provisioners/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var stats = metrics{
	obtain: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_provisioner",
		Subsystem: "acme",
		Name:      "obtain_total",
		Help:      "Number of certificates obtained from the ACME CA, by result once the retries are done",
	}, []string{"result"}),

	notAfter: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_provisioner",
		Subsystem: "acme",
		Name:      "certificate_not_after_seconds",
		Help:      "Expiry of the certificates the provisioner holds, in unix seconds",
	}, []string{"uuid", "name"}),
}

type metrics struct {
	obtain   *prometheus.CounterVec
	notAfter *prometheus.GaugeVec
}

func init() {
	sdkmetrics.MustRegister(config.AppName, stats.obtain)
	sdkmetrics.MustRegister(config.AppName, stats.notAfter)
}

func (m *metrics) IncObtain(err error) {
	result := "success"
	if err != nil {
		result = "failure"
	}
	m.obtain.With(prometheus.Labels{"result": result}).Inc()
}

// SetNotAfter drops the series of the certificate first, so a renamed certificate doesn't keep its old name.
func (m *metrics) SetNotAfter(uuid, name string, notAfter time.Time) {
	m.DeleteNotAfter(uuid)
	m.notAfter.With(prometheus.Labels{"uuid": uuid, "name": name}).Set(float64(notAfter.Unix()))
}

func (m *metrics) DeleteNotAfter(uuid string) {
	m.notAfter.DeletePartialMatch(prometheus.Labels{"uuid": uuid})
}
