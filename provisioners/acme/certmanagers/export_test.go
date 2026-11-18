package certmanagers

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// ObtainCount is the obtain_total counter of a result, for the specs of acme_test.
func ObtainCount(result string) float64 {
	return testutil.ToFloat64(stats.obtain.WithLabelValues(result))
}

// NotAfterSeconds is the certificate_not_after_seconds gauge of a certificate, false when it has no series.
func NotAfterSeconds(uuid, name string) (float64, bool) {
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(stats.notAfter)
	families, err := registry.Gather()
	if err != nil {
		panic(err)
	}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, pair := range metric.GetLabel() {
				labels[pair.GetName()] = pair.GetValue()
			}
			isTheCertificate := labels["uuid"] == uuid && labels["name"] == name
			if isTheCertificate {
				return metric.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

// SetControllerCallTimeout shortens the bound of the calls to the controller until the end of the spec. Call it before
// the manager runs: its goroutines read it.
func SetControllerCallTimeout(timeout time.Duration) {
	previous := controllerCallTimeout
	controllerCallTimeout = timeout
	DeferCleanup(func() { controllerCallTimeout = previous })
}

// DueUuids gives the certificates the next renew tick would order.
func (o *CertManager) DueUuids() []string {
	uuids := make([]string, 0, len(o.listDue()))
	for _, record := range o.listDue() {
		uuids = append(uuids, record.crt.GetUUID())
	}
	return uuids
}

// RecordedDomains gives the domains of the record of a certificate, nil when it has none.
func (o *CertManager) RecordedDomains(crtUuid string) []string {
	record, ok := o.findRecord(crtUuid)
	if !ok {
		return nil
	}
	return record.crt.GetDomainNames()
}

// LockCert holds the lock the orders, renewals and removals of a certificate take, to line up their goroutines.
func (o *CertManager) LockCert(crtUuid string) (unlock func()) {
	return o.lockCert(crtUuid)
}

// SetRemovalKeptFor shortens how long a removal is remembered until the end of the spec. Call it before the manager
// runs: its goroutines read it.
func SetRemovalKeptFor(keptFor time.Duration) {
	previous := removalKeptFor
	removalKeptFor = keptFor
	DeferCleanup(func() { removalKeptFor = previous })
}

// ErrRemoved is the cause of the cancel of an order whose certificate was removed.
var ErrRemoved = errRemoved

// BackOffDelay is the wait before the next order after the given count of failed orders in a row.
var BackOffDelay = backOffDelay

// HasActivity tells whether the manager still keeps a retry, an order or a removal of the certificate.
func (o *CertManager) HasActivity(crtUuid string) bool {
	o.timersMutex.Lock()
	defer o.timersMutex.Unlock()
	_, ok := o.activities[crtUuid]
	return ok
}

func (o *CertManager) FirstRenewDone() <-chan struct{} {
	return o.firstRenewDone
}
