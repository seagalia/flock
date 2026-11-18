package certmanagers

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/cespare/xxhash/v2"
	"github.com/seagalia/flock/provisioners/acme/challenges"
	"github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/sdk/agentsdk/leases"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

// errLeftOrder stops an order with no retry nor back-off, the ADS update of the change orders it again or removes it
var errLeftOrder = errors.New("certificate changed on the controller during its order")

// SetChallenges shares the http-01 and tls-alpn-01 answers of the orders between the replicas through a challenge
// secret per certificate. A nil table, the default, leaves the challenges to lego alone.
func (o *CertManager) SetChallenges(table *challenges.Table) {
	o.challenges = table
}

// prepareChallenges gives the challenge secret of the order, nil when the challenges are not shared. It tells false
// when the order must stop, after scheduling its retry when one is due.
func (o *CertManager) prepareChallenges(
	ctx context.Context, lease *leases.Lease, resolved, stored *cert.Certificate, domains []string,
) (*secret.Secret, bool) {
	if o.challenges == nil {
		return nil, true
	}
	logger := o.entry.With().Str("uuid", stored.GetUUID()).Logger()
	challengeSecret, err := o.ensureChallengeSecret(lease.Context(), lease, stored, domains)
	isLost := errors.Is(err, leases.ErrLost) || errors.Is(context.Cause(lease.Context()), leases.ErrLost)
	switch {
	case err == nil:
		return challengeSecret, true
	case errors.Is(err, errLeftOrder):
		logger.Info().Msg("Certificate changed on the controller before its order, not ordering it")
	case isLost:
		logger.Warn().Err(err).Msg("Lease of the certificate lost before its order, order cancelled")
	case ctx.Err() != nil:
		// the stop or a removal cut the preparation, like an order they cut
	default:
		logger.Warn().Err(err).Msg("Failed to prepare the challenge secret of the certificate, retrying later")
		o.states.AddError(stored, fmt.Errorf("failed to prepare the challenge secret: %w", err))
		o.scheduleRetry(resolved, time.Now().Add(o.retryDelay))
	}
	return nil, false
}

// ensureChallengeSecret gives the challenge secret of the certificate, created on its first order, and makes the
// certificate reference it so ADS delivers the secret to every replica. stored is the copy read under the lease, its
// refs intact. The reference is written onto a copy read right before the write, like the certificate obtained.
func (o *CertManager) ensureChallengeSecret(
	ctx context.Context, lease *leases.Lease, stored *cert.Certificate, domains []string,
) (*secret.Secret, error) {
	crtUuid := stored.GetUUID()
	found, err := o.challengeSecretsOf(ctx, crtUuid)
	if err != nil {
		return nil, err
	}
	var challengeSecret *secret.Secret
	if len(found) > 0 {
		challengeSecret = found[0]
	} else {
		challengeSecret, err = o.createChallengeSecret(ctx, crtUuid)
		if err != nil {
			return nil, err
		}
	}
	// a certificate that left acme and came back finds its secret detached, which the clean up would delete once the
	// certificate is gone while the provider still writes it
	isDetached := challengeSecret.GetMetadata()[mdAttachedKey] != strconv.FormatBool(true)
	if isDetached {
		err = o.attachSecret(ctx, challengeSecret)
		if err != nil {
			return nil, fmt.Errorf("attach challenge secret: %w", err)
		}
	}

	ref := secret.Ref{SecretUUID: challengeSecret.GetUUID(), Key: challenges.MarkerKey}.String()
	// a ref to another secret comes from a certificate cloned with the metadata of another one
	if stored.GetMetadata()[challenges.RefMetadataKey] == ref {
		return challengeSecret, nil
	}
	err = o.writeChallengeRef(ctx, lease, crtUuid, domains, ref)
	if err != nil {
		return nil, err
	}
	return challengeSecret, nil
}

func (o *CertManager) createChallengeSecret(ctx context.Context, crtUuid string) (*secret.Secret, error) {
	name := fmt.Sprintf("flock-acme-challenges-%x", xxhash.Sum64([]byte(crtUuid)))
	keyProv, valueProv := config.MakeProvisionerMetadata(config.ACMEProvisionerName)
	createCtx, cancelCreate := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelCreate()
	resp, err := o.secretClient.CreateSecret(createCtx, &svcsecret.CreateSecretRequest{
		Secret: &secret.Secret{
			Name: name,
			Data: challenges.EncodeData(nil),
			Metadata: map[string]string{
				challenges.MetadataKey:      "true",
				keyProv:                     valueProv,
				helpers.MetadataLockKey:     "true",
				helpers.MetadataProvisionBy: o.agentUuid(),
				mdCertRefKey:                crtUuid,
				mdAttachedKey:               "true",
			},
		},
	})
	// the filters didn't find it, so a secret of that name is not the challenge secret of this certificate
	if connect.CodeOf(err) == connect.CodeAlreadyExists {
		return nil, fmt.Errorf("secret %s exists but is not the challenge secret of the certificate: %w", name, err)
	}
	if err != nil {
		return nil, fmt.Errorf("create challenge secret %s: %w", name, err)
	}
	return resp.GetSecret(), nil
}

func (o *CertManager) writeChallengeRef(
	ctx context.Context, lease *leases.Lease, crtUuid string, domains []string, ref string,
) error {
	err := lease.Renew(ctx)
	if err != nil {
		return fmt.Errorf("renew lease before the challenge reference write: %w", err)
	}
	latest, isStillOrdered, err := o.readBeforeWrite(ctx, crtUuid, domains)
	if err != nil {
		return fmt.Errorf("read certificate before the challenge reference write: %w", err)
	}
	if !isStillOrdered {
		return errLeftOrder
	}
	md := maps.Clone(latest.GetMetadata())
	md[challenges.RefMetadataKey] = ref
	latest.Metadata = md

	updateCtx, cancelUpdate := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelUpdate()
	_, err = o.certClient.UpdateCertificate(updateCtx, &svccert.UpdateCertificateRequest{
		UUID:        crtUuid,
		Certificate: latest,
	})
	if err != nil {
		return fmt.Errorf("write challenge reference: %w", err)
	}
	return nil
}

// challengeSecretsOf lists the challenge secrets of the certificate whoever created them, the replica that did may be
// gone for good
func (o *CertManager) challengeSecretsOf(ctx context.Context, crtUuid string) ([]*secret.Secret, error) {
	listCtx, cancelList := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelList()
	resp, err := o.secretClient.ListSecret(listCtx, &svcsecret.ListSecretRequest{
		// one secret per certificate, the name being fixed, the limit only bounds the answer
		Limit: 10,
		Filters: svccore.ListFilters{
			{
				Key:        mdCertRefKey,
				Value:      crtUuid,
				Operator:   svccore.ListFilter_EQUAL,
				IsMetadata: true,
			},
			{
				Key:        challenges.MetadataKey,
				Value:      strconv.FormatBool(true),
				Operator:   svccore.ListFilter_EQUAL,
				IsMetadata: true,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("list challenge secrets of certificate %s: %w", crtUuid, err)
	}
	return resp.GetSecrets(), nil
}

// releaseChallengeSecrets follows the certificate's own secret: deleted once the certificate is gone, detached while
// it stays, so the clean up deletes it once the certificate is gone. The certificate keeps its reference, and the
// controller refuses to delete a secret still referenced anyway.
// It runs with no table too, so the secrets of a provisioner whose http-01 and tls-alpn-01 were turned off since are
// still freed.
func (o *CertManager) releaseChallengeSecrets(ctx context.Context, crtUuid string, isCertGone bool) error {
	found, err := o.challengeSecretsOf(ctx, crtUuid)
	if err != nil {
		return err
	}
	for _, challengeSecret := range found {
		if !isCertGone {
			err = o.detachSecret(ctx, challengeSecret)
			if err != nil {
				return fmt.Errorf("detach challenge secret: %w", err)
			}
			continue
		}
		deleteCtx, cancelDelete := context.WithTimeout(ctx, controllerCallTimeout)
		_, err = o.secretClient.DeleteSecret(deleteCtx, &svccore.UuidRequest{UUID: challengeSecret.GetUUID()})
		cancelDelete()
		// another replica may have deleted it first
		isSecretGone := connect.CodeOf(err) == connect.CodeNotFound
		if err != nil && !isSecretGone {
			return fmt.Errorf("delete challenge secret: %w", err)
		}
	}
	return nil
}
