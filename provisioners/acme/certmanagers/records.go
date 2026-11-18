package certmanagers

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"golang.org/x/net/idna"
	"google.golang.org/protobuf/proto"
)

type certRecord struct {
	// crt is the resolved ADS copy, its secret refs replaced by their values, which the orders use and no write sends
	crt         *cert.Certificate
	notBefore   time.Time
	notAfter    time.Time
	leafDomains []string
	secretUuid  string
	// queuedOrders keeps the renew tick off a certificate whose orders are still queued, it would order it twice. A
	// count, since an ADS update coming during an order queues a second one.
	queuedOrders int
	// pending holds a certificate obtained whose write to the controller failed, the next attempt writes it instead of
	// ordering again, since each order counts against the rate limits of the CA
	pending *pendingWrite
}

// restoreRecord puts back the dates of the record a failed write changed. It keeps the secret the write may have
// created, so the next write reuses it, and the pending write.
func (o *CertManager) restoreRecord(crtUuid string, previous certRecord) {
	o.rwMutex.Lock()
	record, ok := o.certRecords[crtUuid]
	if ok {
		record.notBefore = previous.notBefore
		record.notAfter = previous.notAfter
		record.leafDomains = previous.leafDomains
	}
	o.rwMutex.Unlock()
	if previous.notAfter.IsZero() {
		stats.DeleteNotAfter(crtUuid)
		return
	}
	stats.SetNotAfter(crtUuid, previous.crt.GetName(), previous.notAfter)
}

// recordIssued puts the dates of the chain issued on the record before its write, and keeps the copy of the record,
// which the ADS updates made during the order may have refreshed
func (o *CertManager) recordIssued(crtUuid string, issued issuedChain) error {
	leaf, err := o.leafFromPEM([]byte(issued.certChain))
	if err != nil {
		return err
	}
	o.rwMutex.Lock()
	record, ok := o.certRecords[crtUuid]
	var name string
	if ok {
		record.notBefore = leaf.NotBefore
		record.notAfter = leaf.NotAfter
		record.leafDomains = leaf.DNSNames
		name = record.crt.GetName()
	}
	o.rwMutex.Unlock()
	if !ok {
		return errors.New("certificate record not found")
	}
	stats.SetNotAfter(crtUuid, name, leaf.NotAfter)
	return nil
}

// recordIfMissing records a certificate ordered before it had a record, one holding a chain and no key, since the write
// of its order needs the record. Recorded with no chain, it stays due until the write.
func (o *CertManager) recordIfMissing(resolved *cert.Certificate) {
	withoutChain := proto.Clone(resolved).(*cert.Certificate)
	withoutChain.CertChain = ""
	withoutChain.PrivateKey = ""
	o.rwMutex.Lock()
	defer o.rwMutex.Unlock()
	_, isRecorded := o.certRecords[resolved.GetUUID()]
	if isRecorded {
		return
	}
	o.certRecords[resolved.GetUUID()] = &certRecord{
		crt:        withoutChain,
		secretUuid: withoutChain.GetMetadata()[mdSecretRefKey],
	}
}

func coversDomains(leafDomains, domains []string) bool {
	for _, domain := range domains {
		normalized := normalizeDomain(domain)
		isCovered := slices.ContainsFunc(leafDomains, func(leafDomain string) bool {
			return normalizeDomain(leafDomain) == normalized
		})
		if !isCovered {
			return false
		}
	}
	return true
}

func sameDomains(a, b []string) bool {
	return coversDomains(a, b) && coversDomains(b, a)
}

// normalizeDomain gives the domain in the form the CA writes it in the leaf: lego punycodes the domains it orders, and
// CAs issue lowercase SANs. A domain idna refuses is left lowercased, the order fails on it anyway.
func normalizeDomain(domain string) string {
	lowered := strings.TrimSuffix(strings.ToLower(domain), ".")
	ascii, err := idna.ToASCII(lowered)
	if err != nil {
		return lowered
	}
	return ascii
}

// isRenewalDue renews with a third of the lifetime left, which leaves room for CA outages whatever the validity of the
// CA
func isRenewalDue(notBefore, notAfter time.Time) bool {
	lifetime := notAfter.Sub(notBefore)
	renewAt := notAfter.Add(-lifetime / 3)
	return time.Now().After(renewAt)
}

func (o *CertManager) dropRecord(crtUuid string) {
	o.rwMutex.Lock()
	delete(o.certRecords, crtUuid)
	o.rwMutex.Unlock()
	stats.DeleteNotAfter(crtUuid)
}

// findRecord gives a copy, the records only change under the lock
func (o *CertManager) findRecord(crtUuid string) (certRecord, bool) {
	o.rwMutex.RLock()
	defer o.rwMutex.RUnlock()
	record, ok := o.certRecords[crtUuid]
	if !ok {
		return certRecord{}, false
	}
	return *record, true
}

// storeInfoNoSecret takes the secret the certificate points at over the one recorded: a replica records a certificate
// with no secret before another replica writes it with one, and creating a second secret would clash on its name
func (o *CertManager) storeInfoNoSecret(certificate *cert.Certificate, ordersQueued int) error {
	secretRef := certificate.GetMetadata()[mdSecretRefKey]
	if secretRef != "" {
		return o.storeInfo(certificate, secretRef, ordersQueued)
	}
	record, _ := o.findRecord(certificate.GetUUID())
	return o.storeInfo(certificate, record.secretUuid, ordersQueued)
}

// storeInfo records a certificate with no chain with zero dates, which makes it due at once. The orders queued are
// counted in the same critical section as the store, a renew tick in between would see the record due.
func (o *CertManager) storeInfo(certificate *cert.Certificate, secretUuid string, ordersQueued int) error {
	certClone := proto.Clone(certificate).(*cert.Certificate)
	record := &certRecord{crt: certClone, secretUuid: secretUuid}
	if certClone.GetCertChain() != "" {
		leaf, err := o.leafFromPEM([]byte(certClone.GetCertChain()))
		if err != nil {
			return err
		}
		record.notBefore = leaf.NotBefore
		record.notAfter = leaf.NotAfter
		record.leafDomains = leaf.DNSNames
	}
	certClone.PrivateKey = ""
	certClone.CertChain = ""
	// once stored, the record is changed in place under the lock, by recordIssued for instance
	notAfter := record.notAfter
	o.rwMutex.Lock()
	// the record replaced may still have orders queued, a write during an order must keep counting them. Its pending
	// write is obtained for its domains, other domains need a new order.
	if previous, ok := o.certRecords[certClone.GetUUID()]; ok {
		record.queuedOrders = previous.queuedOrders
		if sameDomains(previous.crt.GetDomainNames(), certClone.GetDomainNames()) {
			record.pending = previous.pending
		}
	}
	record.queuedOrders += ordersQueued
	o.certRecords[certClone.GetUUID()] = record
	o.rwMutex.Unlock()
	if notAfter.IsZero() {
		stats.DeleteNotAfter(certClone.GetUUID())
		return nil
	}
	stats.SetNotAfter(certClone.GetUUID(), certClone.GetName(), notAfter)
	return nil
}

// needsNewChain tells from the record, since a certificate given by the renew tick has its chain blanked
func (o *CertManager) needsNewChain(crtUuid string) bool {
	record, ok := o.findRecord(crtUuid)
	return !ok || record.needsNewChain()
}

func (r certRecord) needsNewChain() bool {
	hasNoChain := r.notAfter.IsZero()
	return hasNoChain || !coversDomains(r.leafDomains, r.crt.GetDomainNames())
}

func (r certRecord) isDue() bool {
	_, isBackingOff := backOffEnd(r.crt)
	if r.queuedOrders > 0 || isBackingOff {
		return false
	}
	return r.needsNewChain() || isRenewalDue(r.notBefore, r.notAfter)
}

func (o *CertManager) listDue() []certRecord {
	o.rwMutex.RLock()
	defer o.rwMutex.RUnlock()

	var toRenew []certRecord
	for _, record := range o.certRecords {
		if record.isDue() {
			toRenew = append(toRenew, *record)
		}
	}
	return toRenew
}
