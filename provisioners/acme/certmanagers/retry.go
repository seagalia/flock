package certmanagers

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"google.golang.org/protobuf/proto"
)

// errRemoved is the cause of the cancel of an order whose certificate left this provisioner
var errRemoved = errors.New("certificate removed from the provisioner")

// activity is what runs or waits for a certificate, apart from its record, which a removal drops while an order
// may still run
type activity struct {
	retry *time.Timer
	// cancelOrder cancels the order of a certificate removed while it runs
	cancelOrder context.CancelCauseFunc
	// removedAt keeps the certificate removed, so an order queued or waiting for the lock before the removal doesn't
	// record one again
	removedAt time.Time
}

// activityOf is called under timersMutex, and every path clearing a field of the activity calls dropIfIdle after it
func (o *CertManager) activityOf(crtUuid string) *activity {
	current, ok := o.activities[crtUuid]
	if !ok {
		current = &activity{}
		o.activities[crtUuid] = current
	}
	return current
}

func (o *CertManager) dropIfIdle(crtUuid string) {
	current, ok := o.activities[crtUuid]
	if !ok {
		return
	}
	isIdle := current.retry == nil && current.cancelOrder == nil && current.removedAt.IsZero()
	if isIdle {
		delete(o.activities, crtUuid)
	}
}

func (o *CertManager) isMarkedRemoved(crtUuid string) bool {
	current, ok := o.activities[crtUuid]
	return ok && !current.removedAt.IsZero()
}

// markRemoved cancels the order running for the certificate, so the removal, which runs on the ADS handler, doesn't
// wait for an order that can last minutes before it takes the lock of the certificate
func (o *CertManager) markRemoved(crtUuid string) {
	o.timersMutex.Lock()
	defer o.timersMutex.Unlock()
	current := o.activityOf(crtUuid)
	current.removedAt = time.Now()
	if current.retry != nil {
		current.retry.Stop()
		current.retry = nil
	}
	if current.cancelOrder != nil {
		current.cancelOrder(errRemoved)
	}
}

func (o *CertManager) forgetRemoval(crtUuid string) {
	o.timersMutex.Lock()
	defer o.timersMutex.Unlock()
	o.activityOf(crtUuid).removedAt = time.Time{}
	o.dropIfIdle(crtUuid)
}

// pruneRemovals forgets a removal once nothing that started before it can still be waiting. The removal cancels the
// order running, so what waited for the lock of the certificate runs within seconds after it. A removal that failed
// keeps its record, and its tombstone stays as long as the record does, so the renew tick never orders it again.
func (o *CertManager) pruneRemovals() {
	o.rwMutex.RLock()
	recorded := make(map[string]bool, len(o.certRecords))
	for crtUuid := range o.certRecords {
		recorded[crtUuid] = true
	}
	o.rwMutex.RUnlock()

	o.timersMutex.Lock()
	defer o.timersMutex.Unlock()
	for crtUuid, current := range o.activities {
		isRemoved := !current.removedAt.IsZero()
		isOld := time.Since(current.removedAt) > removalKeptFor
		if isRemoved && isOld && !recorded[crtUuid] {
			current.removedAt = time.Time{}
			o.dropIfIdle(crtUuid)
		}
	}
}

func isRemoved(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), errRemoved)
}

// scheduleRetry replaces the retry already scheduled for the certificate. Its order runs under tasks, so the stop
// waits for it, and none starts once the stop began. The order runs from the record, unrecorded is only for a
// certificate with no record.
func (o *CertManager) scheduleRetry(unrecorded *cert.Certificate, at time.Time) {
	crtUuid := unrecorded.GetUUID()
	o.timersMutex.Lock()
	defer o.timersMutex.Unlock()
	if o.stopping || o.isMarkedRemoved(crtUuid) {
		return
	}
	current := o.activityOf(crtUuid)
	if current.retry != nil {
		current.retry.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(time.Until(at), func() {
		o.timersMutex.Lock()
		defer o.timersMutex.Unlock()
		// a timer replaced while it fired waits here for the mutex, and must not clear the timer that replaced it
		fired := o.activityOf(crtUuid)
		if fired.retry == timer {
			fired.retry = nil
		}
		o.dropIfIdle(crtUuid)
		if o.stopping {
			return
		}
		o.tasks.Go(func() {
			unlock := o.lockCert(crtUuid)
			defer unlock()
			o.runOrder(o.runCtx, o.latestCopy(unrecorded), false)
		})
	})
	current.retry = timer
}

// latestCopy is called under the lock of the certificate. OnMessageAdded only refreshes the record of a certificate
// already managed whose domains stay the same, so an edit during the wait of a retry, new DNS credentials for
// instance, is only in the record, and the copy given when the retry was scheduled would order and write over it. A
// certificate with a chain and no key is ordered before it has a record, and only has that copy.
func (o *CertManager) latestCopy(unrecorded *cert.Certificate) *cert.Certificate {
	record, ok := o.findRecord(unrecorded.GetUUID())
	if !ok {
		return unrecorded
	}
	return proto.Clone(record.crt).(*cert.Certificate)
}

func (o *CertManager) cancelRetry(crtUuid string) {
	o.timersMutex.Lock()
	defer o.timersMutex.Unlock()
	current := o.activityOf(crtUuid)
	if current.retry != nil {
		current.retry.Stop()
		current.retry = nil
	}
	o.dropIfIdle(crtUuid)
}

// stopRetries makes sure no retry starts an order once it returned, so the tasks to wait for no longer grow
func (o *CertManager) stopRetries() {
	o.timersMutex.Lock()
	defer o.timersMutex.Unlock()
	o.stopping = true
	for crtUuid, current := range o.activities {
		if current.retry != nil {
			current.retry.Stop()
			current.retry = nil
		}
		o.dropIfIdle(crtUuid)
	}
}

func (o *CertManager) lockCert(crtUuid string) (unlock func()) {
	certLock, _ := o.certLocks.LoadOrStore(crtUuid, &sync.Mutex{})
	mutex := certLock.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

// remove runs once markRemoved cancelled the order in flight, so the lock only waits for that order to give its lease
// back, or to finish a write it already sent. The record goes away whether the controller still has the certificate
// or not. It left this provisioner, and a record kept would have every replica order it again, over the chain the user
// put in its place.
func (o *CertManager) remove(ctx context.Context, certificate *cert.Certificate) error {
	unlock := o.lockCert(certificate.GetUUID())
	defer unlock()
	// a removal failing keeps the record, and a write of the pending certificate after the user adds it back would
	// put back a chain the user may have replaced meanwhile
	o.dropPending(certificate.GetUUID())
	record, ok := o.findRecord(certificate.GetUUID())
	if !ok {
		return nil
	}
	readCtx, cancelRead := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelRead()
	_, err := o.certClient.ReadCertificate(readCtx, &svccore.UuidRequest{
		UUID: certificate.GetUUID(),
	})
	isCertGone := connect.CodeOf(err) == connect.CodeNotFound
	if err != nil && !isCertGone {
		return fmt.Errorf("failed to read certificate: %w", err)
	}
	own, err := o.readOwnSecret(ctx, certificate.GetUUID(), record.secretUuid)
	if err != nil {
		return err
	}
	// the certificate still in use keeps its secret, detached so the clean up frees it once the certificate is gone
	if own != nil && !isCertGone {
		err = o.detachSecret(ctx, own)
		if err != nil {
			return fmt.Errorf("failed to detach secret: %w", err)
		}
	}
	if own != nil && isCertGone {
		deleteCtx, cancelDelete := context.WithTimeout(ctx, controllerCallTimeout)
		defer cancelDelete()
		_, err := o.secretClient.DeleteSecret(deleteCtx, &svccore.UuidRequest{
			UUID: own.GetUUID(),
		})
		// another replica may have deleted it first
		isSecretGone := connect.CodeOf(err) == connect.CodeNotFound
		if err != nil && !isSecretGone {
			return err
		}
	}
	err = o.releaseChallengeSecrets(ctx, certificate.GetUUID(), isCertGone)
	if err != nil {
		return err
	}
	o.dropRecord(certificate.GetUUID())
	return nil
}
