package certmanagers

import (
	"context"
	"strconv"
	"time"

	"github.com/seagalia/flock/sdk/agentsdk/leases"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	"google.golang.org/protobuf/proto"
)

// backOff gives the time of the next order and writes it as retry_after onto the copy read right before the write, see
// readBeforeWrite. stored, the copy read before the order, gives the count of failed orders. The lease is renewed
// first, a write from a holder that lost it would replace the certificate another replica just wrote. Without the
// write, this replica still waits the back-off.
func (o *CertManager) backOff(
	ctx context.Context, lease *leases.Lease, stored *cert.Certificate, domains []string, domainsChanged bool,
) time.Time {
	crtUuid := stored.GetUUID()
	logger := o.entry.With().Str("uuid", crtUuid).Logger()
	failures := 0
	if !domainsChanged {
		// a count that doesn't parse starts again from the first delay
		failures, _ = strconv.Atoi(stored.GetMetadata()[mdFailuresKey])
	}
	failures++
	retryAfter := time.Now().Add(backOffDelay(failures))

	err := lease.Renew(ctx)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to renew the lease before writing the back-off, only this replica waits it")
		return retryAfter
	}
	latest, isStillOrdered, err := o.readBeforeWrite(ctx, crtUuid, domains)
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to read the certificate before writing the back-off, only this replica waits it")
		return retryAfter
	}
	if !isStillOrdered {
		logger.Debug().Msg("Certificate changed on the controller during its order, no back-off written")
		return retryAfter
	}
	withBackOff := proto.Clone(latest).(*cert.Certificate)
	if withBackOff.Metadata == nil {
		withBackOff.Metadata = map[string]string{}
	}
	withBackOff.Metadata[mdFailuresKey] = strconv.Itoa(failures)
	withBackOff.Metadata[mdRetryAfterKey] = retryAfter.UTC().Format(time.RFC3339)
	updateCtx, cancelUpdate := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelUpdate()
	_, err = o.certClient.UpdateCertificate(updateCtx, &svccert.UpdateCertificateRequest{
		UUID:        crtUuid,
		Certificate: withBackOff,
	})
	if err != nil {
		logger.Warn().Err(err).Msg("Failed to write the back-off, only this replica waits it")
	}
	return retryAfter
}

func backOffDelay(failures int) time.Duration {
	delay := firstBackOff
	for range failures - 1 {
		delay *= 2
		if delay >= maxBackOff {
			return maxBackOff
		}
	}
	return delay
}

// backOffEnd gives the retry_after of the certificate while it is still to come. A value that doesn't parse holds
// nothing back, so a typo of the user can't stop the orders of the certificate.
func backOffEnd(certificate *cert.Certificate) (time.Time, bool) {
	retryAfter, err := time.Parse(time.RFC3339, certificate.GetMetadata()[mdRetryAfterKey])
	if err != nil {
		return time.Time{}, false
	}
	return retryAfter, retryAfter.After(time.Now())
}
