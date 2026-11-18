package certmanagers

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/avast/retry-go/v5"
	"github.com/go-acme/lego/v5/certcrypto"
	legocert "github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-viper/mapstructure/v2"
	"github.com/seagalia/flock/provisioners/acme/challenges"
	"github.com/seagalia/flock/provisioners/acme/params"
	"github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/sdk/agentsdk/leases"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

// queuedOrder tells the domains changed as OnMessageAdded saw them, the record of a certificate with no chain takes the
// new domains before its order runs
type queuedOrder struct {
	crt            *cert.Certificate
	domainsChanged bool
}

// issuedChain is what the CA gave for an order
type issuedChain struct {
	certChain  string
	privateKey string
}

// onto gives a clone of certificate holding the chain and key issued
func (i issuedChain) onto(certificate *cert.Certificate) *cert.Certificate {
	withChain := proto.Clone(certificate).(*cert.Certificate)
	withChain.CertChain = i.certChain
	withChain.PrivateKey = i.privateKey
	return withChain
}

// obtained is what an order hands to its write. It holds no certificate, so the values the ads handler resolved in the
// ADS copy can't reach a write: only the request decoded from them travels, and writeBack turns it into secret refs.
type obtained struct {
	domains []string
	// req is completed by the order, with the account registered on a first order
	req *params.CertRequest
	issuedChain
}

type pendingWrite struct {
	obtained
	// failures counts the failed writes in a row, the retries move to the back-off after writeFailuresBeforeBackOff
	failures int
}

// runOrder is called under the lock of the certificate. It orders nothing for a certificate removed, and registers the
// cancel of the order in the same section as that check, so a removal either sees the order to cancel or is seen by it.
func (o *CertManager) runOrder(ctx context.Context, certificate *cert.Certificate, domainsChanged bool) {
	crtUuid := certificate.GetUUID()
	orderCtx, cancelOrder := context.WithCancelCause(ctx)
	defer cancelOrder(nil)
	o.timersMutex.Lock()
	isRemoved := o.isMarkedRemoved(crtUuid)
	if !isRemoved {
		o.activityOf(crtUuid).cancelOrder = cancelOrder
	}
	o.timersMutex.Unlock()
	if isRemoved {
		return
	}
	defer func() {
		o.timersMutex.Lock()
		o.activityOf(crtUuid).cancelOrder = nil
		o.dropIfIdle(crtUuid)
		o.timersMutex.Unlock()
	}()
	o.orderUnderLease(orderCtx, certificate, domainsChanged)
}

func (o *CertManager) obtainQueued(ctx context.Context, order queuedOrder) {
	certificate := order.crt
	unlock := o.lockCert(certificate.GetUUID())
	defer unlock()
	defer o.addQueuedOrders(certificate.GetUUID(), -1)

	record, isRecorded := o.findRecord(certificate.GetUUID())
	if isRecorded && !record.needsNewChain() {
		return
	}

	o.runOrder(ctx, certificate, order.domainsChanged)
}

// orderUnderLease orders the certificate only while this provisioner holds its lease from the controller, so that a
// single replica orders it. Without the lease it retries later from a timer, which holds no task while it waits.
// A certificate whose domains changed is ordered at once whatever its back-off, since fixing the domains is the usual
// cause of a failure. A certificate obtained whose write failed is written with no new order, whatever the back-off of
// an earlier failed order, since the CA already issued it.
// resolved is the ADS copy, its secret refs replaced by their values by the ads handler. The order decodes its request
// from it, and past that point it only schedules retries, so none of its values can reach a write. stored is the copy
// read under the lease, still holding the refs: it decides whether to order and gives the back-off state.
func (o *CertManager) orderUnderLease(ctx context.Context, resolved *cert.Certificate, domainsChanged bool) {
	logger := o.entry.With().Str("uuid", resolved.GetUUID()).Logger()
	o.cancelRetry(resolved.GetUUID())

	lease, isGranted := o.acquireLease(ctx, resolved)
	if !isGranted {
		return
	}
	defer func() {
		releaseCtx, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), controllerCallTimeout)
		defer cancelRelease()
		if err := lease.Release(releaseCtx); err != nil {
			logger.Warn().Err(err).Msg("Failed to release the lease of the certificate, it ends with its ttl")
		}
	}()

	stored, isOrderNeeded, err := o.isOrderNeeded(ctx, resolved)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to read the certificate from the controller, retrying later")
		o.scheduleRetry(resolved, time.Now().Add(o.retryDelay))
		return
	}
	if !isOrderNeeded {
		// a copy with other domains only means a newer ADS copy is on its way, which drops the pending write itself
		hasFreshChain := sameDomains(stored.GetDomainNames(), resolved.GetDomainNames())
		if hasFreshChain {
			o.dropPending(resolved.GetUUID())
		}
		return
	}
	pending, isPending := o.usablePending(resolved)
	if isPending {
		retryAt, isRetried := o.writeUnderLease(ctx, lease, stored, pending.obtained)
		if isRetried {
			o.scheduleRetry(resolved, retryAt)
		}
		return
	}
	retryAfter, isBackingOff := backOffEnd(stored)
	if isBackingOff && !domainsChanged {
		logger.Debug().Time("retry_after", retryAfter).Msg("Certificate backing off after a failed order")
		o.scheduleRetry(resolved, retryAfter)
		return
	}

	o.recordIfMissing(resolved)
	domains := slices.Clone(resolved.GetDomainNames())
	req, err := certRequestOf(resolved, o.defaultEmail)
	// a request that doesn't decode fails like an order, the back-off holds it until the user fixes the certificate
	var issued issuedChain
	if err == nil {
		challengeSecret, isPrepared := o.prepareChallenges(ctx, lease, resolved, stored, domains)
		if !isPrepared {
			return
		}
		issued, err = o.obtainWithRetry(lease.Context(), stored, domains, req, challengeSecret)
	}
	isLost := errors.Is(context.Cause(lease.Context()), leases.ErrLost)
	var retryAt time.Time
	isRetried := false
	switch {
	case err == nil:
		stats.IncObtain(nil)
		result := obtained{domains: domains, req: req, issuedChain: issued}
		retryAt, isRetried = o.writeUnderLease(ctx, lease, stored, result)
	case isLost:
		logger.Warn().Err(err).Msg("Lease of the certificate lost, order cancelled")
	case ctx.Err() != nil:
		// the stop cut the order, and the next start orders it again, or a removal cut it for good
	default:
		stats.IncObtain(err)
		o.states.AddError(stored, fmt.Errorf("failed to obtain certificate: %w", err))
		retryAt = o.backOff(ctx, lease, stored, domains, domainsChanged)
		isRetried = true
		logger.Error().Err(err).Msg("Failed to obtain certificate, backing off")
	}
	if isRetried {
		o.scheduleRetry(resolved, retryAt)
	}
}

// certRequestOf is the only step of an order reading the values the ads handler resolved
func certRequestOf(resolved *cert.Certificate, defaultEmail string) (*params.CertRequest, error) {
	if len(resolved.GetDomainNames()) == 0 {
		return nil, errors.New("no domains")
	}
	req := &params.CertRequest{}
	err := mapstructure.Decode(resolved.GetMetadata(), req)
	if err != nil {
		return nil, fmt.Errorf("decode metadata: %w", err)
	}
	if req.Email == "" {
		req.Email = defaultEmail
	}
	return req, nil
}

// acquireLease schedules the retry of the order when the lease is not granted. A certificate the controller no longer
// has, or no longer lets this provisioner lease, gets no retry, which would otherwise loop until the next start. A
// lease held by another replica is retried only for a certificate with no chain for its domains, a first order or a
// domain change, which would otherwise wait for the renew tick.
func (o *CertManager) acquireLease(ctx context.Context, certificate *cert.Certificate) (*leases.Lease, bool) {
	logger := o.entry.With().Str("uuid", certificate.GetUUID()).Logger()
	lease, err := o.leases.Acquire(ctx, helpers.TypeUrl[*cert.Certificate](), certificate.GetUUID(),
		leases.WithTTL(o.leaseTTL))
	if err == nil {
		return lease, true
	}
	var held *leases.HeldError
	isHeld := errors.As(err, &held)
	code := connect.CodeOf(err)
	isRefused := code == connect.CodeNotFound || code == connect.CodePermissionDenied
	switch {
	case isHeld && o.needsNewChain(certificate.GetUUID()):
		logger.Debug().Str("holder", held.HolderName).Time("expires_at", held.ExpiresAt).
			Msg("Certificate leased by another provisioner, retrying at the end of its lease")
		o.scheduleRetry(certificate, held.ExpiresAt)
	case isHeld:
		// the holder writes its back-off when its renewal fails, and the renew tick orders again for a holder gone
		logger.Debug().Str("holder", held.HolderName).
			Msg("Certificate leased by another provisioner, leaving its renewal to the holder")
	case ctx.Err() != nil:
		// the stop cut the call, the next start orders it again
	case isRefused:
		logger.Error().Err(err).Msg("Controller refuses the lease of the certificate, not ordering it")
	default:
		logger.Warn().Err(err).Msg("Failed to take the lease of the certificate, retrying later")
		o.scheduleRetry(certificate, time.Now().Add(o.retryDelay))
	}
	return nil, false
}

// writeUnderLease writes the certificate obtained only while the lease is still ours. It writes onto the copy read
// right before the write, see readBeforeWrite. A write that fails keeps the certificate obtained as pending and gives
// the time of its retry, which reads the controller again and writes it with no new order. The record goes back to what
// it was, so the renew tick sees the certificate as due again too. stored, the copy read before the order, only names
// the certificate in the state store.
func (o *CertManager) writeUnderLease(
	ctx context.Context, lease *leases.Lease, stored *cert.Certificate, result obtained,
) (time.Time, bool) {
	crtUuid := stored.GetUUID()
	logger := o.entry.With().Str("uuid", crtUuid).Logger()
	// an order finishing after the stop still writes its certificate, it must not lose it to a cancelled Renew
	writeCtx, cancelWrite := context.WithTimeout(context.WithoutCancel(ctx), writeBackTimeout)
	defer cancelWrite()
	if isRemoved(ctx) {
		return time.Time{}, false
	}
	err := lease.Renew(writeCtx)
	if errors.Is(err, leases.ErrLost) {
		logger.Warn().Err(err).Msg("Lease of the certificate lost before its write, certificate obtained dropped")
		o.dropPending(crtUuid)
		return time.Time{}, false
	}
	if err != nil {
		return o.retryWrite(ctx, stored, result, fmt.Errorf("renew lease before the write: %w", err))
	}
	latest, isStillOrdered, err := o.readBeforeWrite(writeCtx, crtUuid, result.domains)
	if err != nil {
		return o.retryWrite(ctx, stored, result, fmt.Errorf("read certificate before the write: %w", err))
	}
	// the write replaces the whole certificate, a write after the removal would put back the provisioner metadata the
	// user just took off it. The write ctx is detached, so ctx is read again.
	if isRemoved(ctx) {
		return time.Time{}, false
	}
	if !isStillOrdered {
		logger.Info().Msg("Certificate changed on the controller during its order, certificate obtained dropped")
		o.dropPending(crtUuid)
		return time.Time{}, false
	}
	previous, _ := o.findRecord(crtUuid)
	err = o.recordIssued(crtUuid, result.issuedChain)
	if err != nil {
		return o.retryWrite(ctx, stored, result, fmt.Errorf("record certificate obtained: %w", err))
	}
	err = o.writeBack(writeCtx, latest, result)
	if err != nil {
		o.restoreRecord(crtUuid, previous)
		return o.retryWrite(ctx, stored, result, fmt.Errorf("write certificate back: %w", err))
	}
	o.dropPending(crtUuid)
	o.states.ClearState(helpers.TypeUrl[*cert.Certificate](), crtUuid)
	return time.Time{}, false
}

// retryWrite keeps the certificate obtained for the next attempt and gives the time of that attempt, unless the
// certificate was removed meanwhile
func (o *CertManager) retryWrite(
	ctx context.Context, stored *cert.Certificate, result obtained, err error,
) (time.Time, bool) {
	if isRemoved(ctx) {
		return time.Time{}, false
	}
	failures := o.keepPending(stored.GetUUID(), result)
	delay := o.retryDelay
	if failures >= writeFailuresBeforeBackOff {
		delay = backOffDelay(failures - writeFailuresBeforeBackOff + 1)
	}
	o.states.AddError(stored, fmt.Errorf("failed to write certificate obtained: %w", err))
	o.entry.Error().Err(err).Str("uuid", stored.GetUUID()).Int("failures", failures).Dur("retry_in", delay).
		Msg("Failed to write the certificate obtained, writing it again later with no new order")
	return time.Now().Add(delay), true
}

// readBeforeWrite reads the certificate again right before a write, since an order lasts up to ~30 minutes with its
// retries and a write replaces the whole certificate: a write onto the copy read before the order would revert an edit
// the user made meanwhile. A certificate gone, holding other domains than the ones ordered, or no longer given to this
// provisioner gets no write, the ADS update of that change orders it again or removes it.
func (o *CertManager) readBeforeWrite(
	ctx context.Context, crtUuid string, domains []string,
) (*cert.Certificate, bool, error) {
	readCtx, cancelRead := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelRead()
	resp, err := o.certClient.ReadCertificate(readCtx, &svccore.UuidRequest{UUID: crtUuid})
	if connect.CodeOf(err) == connect.CodeNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read certificate: %w", err)
	}
	latest := resp.GetCertificate()
	provisionerKey, provisionerValue := config.MakeProvisionerMetadata(config.ACMEProvisionerName)
	isProvisioned := latest.GetMetadata()[provisionerKey] == provisionerValue
	isForDomains := sameDomains(latest.GetDomainNames(), domains)
	return latest, isProvisioned && isForDomains, nil
}

// isOrderNeeded reads the certificate again from the controller, since the holder that just released its lease may
// have written it and a queued ADS copy is older than the lease. A copy with other domains means a newer ADS copy is
// on its way, which orders it.
func (o *CertManager) isOrderNeeded(
	ctx context.Context, resolved *cert.Certificate,
) (*cert.Certificate, bool, error) {
	readCtx, cancelRead := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelRead()
	resp, err := o.certClient.ReadCertificate(readCtx, &svccore.UuidRequest{UUID: resolved.GetUUID()})
	if connect.CodeOf(err) == connect.CodeNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read certificate: %w", err)
	}
	stored := resp.GetCertificate()
	if !sameDomains(stored.GetDomainNames(), resolved.GetDomainNames()) {
		return stored, false, nil
	}
	if stored.GetCertChain() == "" {
		return stored, true, nil
	}
	leaf, err := o.leafFromPEM([]byte(stored.GetCertChain()))
	if err != nil {
		return stored, true, nil
	}
	// the controller gets the new domains at once and the new chain only after the order, so equal domains alone
	// don't prove the chain is for them
	isDue := isRenewalDue(leaf.NotBefore, leaf.NotAfter)
	return stored, !coversDomains(leaf.DNSNames, resolved.GetDomainNames()) || isDue, nil
}

// obtainWithRetry gives each attempt its own obtainTimeout, a slow first attempt doesn't eat the time of the next ones.
// The attempts share req, so an account registered by a failed attempt is reused by the next one, and the challenge
// provider, which takes out of the secret the challenges of a failed attempt. It writes nothing but the challenges.
func (o *CertManager) obtainWithRetry(
	ctx context.Context, stored *cert.Certificate, domains []string, req *params.CertRequest,
	challengeSecret *secret.Secret,
) (issuedChain, error) {
	o.states.SetState(stored, agentstatus.StatusResponse_ADDING)
	retrier := retry.New(
		retry.Context(ctx),
		retry.Attempts(o.retryAttempts),
		retry.Delay(o.retryDelay),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.OnRetry(func(attempt uint, err error) {
			o.entry.Debug().Err(err).Uint("attempt", attempt).Str("uuid", stored.GetUUID()).Msg("Retrying to obtain certificate")
			o.states.AddError(stored, fmt.Errorf("retrying to obtain certificate: %w", err))
		}),
	)
	request := legocert.ObtainRequest{
		Domains: domains,
		KeyType: certcrypto.KeyType(req.KeyType),
		Bundle:  true,
	}
	var provider *challenges.Provider
	// a nil *challenges.Provider in the interface would not read as nil to the factory
	var solver challenge.Provider
	if challengeSecret != nil {
		provider = challenges.NewProvider(o.secretClient, o.challenges, challengeSecret, challenges.DefaultEchoTimeout)
		solver = provider
	}
	var res *legocert.Resource
	err := retrier.Do(func() error {
		attemptCtx, cancel := context.WithTimeout(ctx, obtainTimeout)
		defer cancel()
		var err error
		res, err = o.obtainer.Obtain(attemptCtx, req, request, solver)
		return err
	})
	// a challenge lego never cleaned up would keep blocking the tls-alpn-01 of its domain for the other certificates
	if provider != nil {
		o.flushChallenges(ctx, provider, stored.GetUUID())
	}
	if err != nil {
		return issuedChain{}, err
	}
	return issuedChain{certChain: string(res.Certificate), privateKey: string(res.PrivateKey)}, nil
}

func (o *CertManager) flushChallenges(ctx context.Context, provider *challenges.Provider, crtUuid string) {
	err := provider.Flush(ctx)
	if err != nil {
		o.entry.Warn().Err(err).Str("uuid", crtUuid).Msg("Failed to clear the challenges left by the order")
	}
}

// writeBack keeps the account in its secret and writes the chain onto stored, a copy read from the controller, which
// still holds the secret refs. Only what the order produced goes onto it, so no value the ads handler resolved goes
// back to the controller in clear.
func (o *CertManager) writeBack(ctx context.Context, stored *cert.Certificate, result obtained) error {
	secretUuid, err := o.addOrUpdateSecret(ctx, stored.GetUUID(), result.req)
	if err != nil {
		return fmt.Errorf("failed to add or update secret: %w", err)
	}

	refs, err := result.req.ToMapSecretRef(secretUuid)
	if err != nil {
		return fmt.Errorf("failed to convert certificate request to secret map: %w", err)
	}

	written := result.onto(stored)
	md := maps.Clone(written.GetMetadata())
	if md == nil {
		md = map[string]string{}
	}
	for key, value := range refs {
		// the email, CA url and key type come in clear from the request decoded from the resolved copy, a ref the
		// user put in their place stays
		isUserRef := strings.HasPrefix(md[key], secret.RefPrefix) && !strings.HasPrefix(value, secret.RefPrefix)
		if !isUserRef {
			md[key] = value
		}
	}
	md[mdSecretRefKey] = secretUuid
	md[helpers.MetadataProvisionBy] = o.agentUuid()
	delete(md, mdRetryAfterKey)
	delete(md, mdFailuresKey)
	written.Metadata = md

	updateCtx, cancelUpdate := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelUpdate()
	_, err = o.certClient.UpdateCertificate(updateCtx, &svccert.UpdateCertificateRequest{
		UUID:        written.GetUUID(),
		Certificate: written,
	})
	return err
}

// addQueuedOrders never goes below zero: a certificate queued before it had a record was never counted
func (o *CertManager) addQueuedOrders(crtUuid string, delta int) {
	o.rwMutex.Lock()
	defer o.rwMutex.Unlock()
	record, ok := o.certRecords[crtUuid]
	if ok {
		record.queuedOrders = max(record.queuedOrders+delta, 0)
	}
}

// keepPending gives the count of failed writes in a row of the certificate obtained
func (o *CertManager) keepPending(crtUuid string, result obtained) int {
	o.rwMutex.Lock()
	defer o.rwMutex.Unlock()
	record, ok := o.certRecords[crtUuid]
	if !ok {
		return 1
	}
	failures := 1
	isSameChain := record.pending != nil && record.pending.certChain == result.certChain
	if isSameChain {
		failures = record.pending.failures + 1
	}
	record.pending = &pendingWrite{obtained: result, failures: failures}
	return failures
}

func (o *CertManager) dropPending(crtUuid string) {
	o.rwMutex.Lock()
	defer o.rwMutex.Unlock()
	record, ok := o.certRecords[crtUuid]
	if ok {
		record.pending = nil
	}
}

// usablePending drops a pending write obtained for other domains, or whose leaf is itself due, a new order replaces it
func (o *CertManager) usablePending(certificate *cert.Certificate) (*pendingWrite, bool) {
	record, ok := o.findRecord(certificate.GetUUID())
	if !ok || record.pending == nil {
		return nil, false
	}
	pending := record.pending
	isForDomains := sameDomains(pending.domains, certificate.GetDomainNames())
	leaf, err := o.leafFromPEM([]byte(pending.certChain))
	isLeafDue := err != nil || isRenewalDue(leaf.NotBefore, leaf.NotAfter)
	if !isForDomains || isLeafDue {
		o.dropPending(certificate.GetUUID())
		return nil, false
	}
	return pending, true
}
