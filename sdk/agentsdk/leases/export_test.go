package leases

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
)

// SetWaitPollMax shortens the longest wait between two Acquire calls of WithWait until the end of the spec.
func SetWaitPollMax(d time.Duration) {
	previous := waitPollMax
	waitPollMax = d
	DeferCleanup(func() { waitPollMax = previous })
}

// KnownExpiry is the last expiry the controller answered for the lease.
func (l *Lease) KnownExpiry() time.Time {
	return l.knownExpiry()
}

// SetMinTTL lowers the smallest ttl Acquire asks until the end of the spec.
func SetMinTTL(d time.Duration) {
	previous := minTTL
	minTTL = d
	DeferCleanup(func() { minTTL = previous })
}

// RenewalPeriod is the wait between two renewals of the lease.
func (l *Lease) RenewalPeriod() time.Duration {
	return l.renewalPeriod()
}
