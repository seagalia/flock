package certmanagers

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"

	"connectrpc.com/connect/v2"
	"github.com/cespare/xxhash/v2"
	"github.com/seagalia/flock/provisioners/acme/params"
	"github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"golang.org/x/sync/errgroup"
)

func (o *CertManager) hashSecretName(crtUuid string) string {
	return fmt.Sprintf("flock-acme-%x", xxhash.Sum64([]byte(crtUuid)))
}

func (o *CertManager) detachSecret(ctx context.Context, own *secret.Secret) error {
	if own.Metadata == nil {
		own.Metadata = map[string]string{}
	}
	own.Metadata[mdAttachedKey] = strconv.FormatBool(false)
	updateCtx, cancelUpdate := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelUpdate()
	_, err := o.secretClient.UpdateSecret(updateCtx, &svcsecret.UpdateSecretRequest{
		UUID:   own.GetUUID(),
		Secret: own,
	})
	if err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}
	return nil
}

// attachSecret takes back a secret detached by a removal, for a certificate given to the provisioner again
func (o *CertManager) attachSecret(ctx context.Context, own *secret.Secret) error {
	if own.Metadata == nil {
		own.Metadata = map[string]string{}
	}
	own.Metadata[mdAttachedKey] = strconv.FormatBool(true)
	own.Metadata[helpers.MetadataProvisionBy] = o.agentUuid()
	updateCtx, cancelUpdate := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelUpdate()
	_, err := o.secretClient.UpdateSecret(updateCtx, &svcsecret.UpdateSecretRequest{
		UUID:   own.GetUUID(),
		Secret: own,
	})
	if err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}
	return nil
}

// addOrUpdateSecret writes the account of the CA into the certificate's own secret, the one recorded whose cert ref
// names it. The secret recorded may belong to another certificate, when a user cloned one with its acme metadata, and
// writing into it would hand that certificate's account to the clone.
func (o *CertManager) addOrUpdateSecret(ctx context.Context, crtUuid string, req *params.CertRequest) (string, error) {
	record, ok := o.findRecord(crtUuid)
	if !ok {
		return "", errors.New("certificate record not found")
	}
	secretMap, err := req.ToSecretMap()
	if err != nil {
		return "", fmt.Errorf("failed to convert certificate request to secret map: %w", err)
	}
	own, err := o.readOwnSecret(ctx, crtUuid, record.secretUuid)
	if err != nil {
		return "", err
	}
	if own == nil {
		return o.createSecretFreeingName(ctx, crtUuid, secretMap)
	}
	o.setSecretUuid(crtUuid, own.GetUUID())

	own.Data = secretMap
	if own.Metadata == nil {
		own.Metadata = map[string]string{}
	}
	own.Metadata[mdAttachedKey] = strconv.FormatBool(true)
	// this can be provisioned by another agent
	own.Metadata[helpers.MetadataProvisionBy] = o.agentUuid()

	updateCtx, cancelUpdate := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelUpdate()
	_, err = o.secretClient.UpdateSecret(updateCtx, &svcsecret.UpdateSecretRequest{
		UUID:   own.GetUUID(),
		Secret: own,
	})
	if err != nil {
		return "", fmt.Errorf("failed to update secret: %w", err)
	}
	return own.GetUUID(), nil
}

// readOwnSecret gives nil when the certificate has no secret of its own: none recorded, gone, or the secret of another
// certificate, which a certificate cloned with the acme metadata of another one points at
func (o *CertManager) readOwnSecret(ctx context.Context, crtUuid, secretUuid string) (*secret.Secret, error) {
	if secretUuid == "" {
		return nil, nil
	}
	readCtx, cancelRead := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelRead()
	resp, err := o.secretClient.ReadSecret(readCtx, &svccore.UuidRequest{UUID: secretUuid})
	if connect.CodeOf(err) == connect.CodeNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read secret: %w", err)
	}
	if resp.GetSecret().GetMetadata()[mdCertRefKey] != crtUuid {
		o.entry.Debug().Str("uuid", crtUuid).Str("secret_uuid", secretUuid).
			Msg("Secret recorded belongs to another certificate, leaving it alone")
		return nil, nil
	}
	return resp.GetSecret(), nil
}

func (o *CertManager) createSecret(
	ctx context.Context, crtUuid, name string, secretMap map[string]string,
) (string, error) {
	keyProv, valueProv := config.MakeProvisionerMetadata(config.ACMEProvisionerName)
	createCtx, cancelCreate := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelCreate()
	resp, err := o.secretClient.CreateSecret(createCtx, &svcsecret.CreateSecretRequest{
		Secret: &secret.Secret{
			Name: name,
			Data: secretMap,
			Metadata: map[string]string{
				keyProv:                     valueProv,
				helpers.MetadataLockKey:     "true",
				helpers.MetadataProvisionBy: o.agentUuid(),
				mdCertRefKey:                crtUuid,
				mdAttachedKey:               "true",
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create secret: %w", err)
	}
	o.setSecretUuid(crtUuid, resp.GetSecret().GetUUID())
	return resp.GetSecret().GetUUID(), nil
}

// createSecretFreeingName deletes a secret already holding the name and creates it again, never writing into it. A
// replica gone before writing the certificate that points at it leaves nothing worth keeping, and a user able to create
// secrets could have planted it to receive the account of the CA and the credentials of the DNS provider. A secret the
// controller won't delete, still referenced or not ours to delete, leaves the name taken, so the secret takes a fresh
// one: the fixed name only keeps two replicas from creating a secret each, and the lease already makes this one the
// only writer.
func (o *CertManager) createSecretFreeingName(
	ctx context.Context, crtUuid string, secretMap map[string]string,
) (string, error) {
	name := o.hashSecretName(crtUuid)
	secretUuid, err := o.createSecret(ctx, crtUuid, name, secretMap)
	isNameTaken := connect.CodeOf(err) == connect.CodeAlreadyExists
	if !isNameTaken {
		return secretUuid, err
	}
	err = o.deleteSecretNamed(ctx, name)
	code := connect.CodeOf(err)
	isNameKept := code == connect.CodeFailedPrecondition || code == connect.CodePermissionDenied
	if isNameKept {
		o.entry.Warn().Err(err).Str("uuid", crtUuid).Msg("Secret name of the certificate taken for good, using a fresh one")
		return o.createSecret(ctx, crtUuid, fmt.Sprintf("%s-%08x", name, rand.Uint32()), secretMap)
	}
	if err != nil {
		return "", err
	}
	return o.createSecret(ctx, crtUuid, name, secretMap)
}

func (o *CertManager) deleteSecretNamed(ctx context.Context, name string) error {
	listCtx, cancelList := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelList()
	resp, err := o.secretClient.ListSecret(listCtx, &svcsecret.ListSecretRequest{
		Limit: 1,
		Filters: []*svccore.ListFilter{{
			Key:      "name",
			Value:    name,
			Operator: svccore.ListFilter_EQUAL,
		}},
	})
	if err != nil {
		return fmt.Errorf("failed to find the secret named %s: %w", name, err)
	}
	if len(resp.GetSecrets()) == 0 {
		return fmt.Errorf("secret named %s already exists but can't be listed", name)
	}
	deleteCtx, cancelDelete := context.WithTimeout(ctx, controllerCallTimeout)
	defer cancelDelete()
	_, err = o.secretClient.DeleteSecret(deleteCtx, &svccore.UuidRequest{UUID: resp.GetSecrets()[0].GetUUID()})
	if err != nil && connect.CodeOf(err) != connect.CodeNotFound {
		return fmt.Errorf("failed to delete the secret named %s: %w", name, err)
	}
	return nil
}

func (o *CertManager) setSecretUuid(crtUuid, secretUuid string) {
	o.rwMutex.Lock()
	defer o.rwMutex.Unlock()
	record, ok := o.certRecords[crtUuid]
	if ok {
		record.secretUuid = secretUuid
	}
}

func (o *CertManager) listDetachedSecret(ctx context.Context) ([]*secret.Secret, error) {
	var offset = 0
	var limit = 100
	var secretsList []*secret.Secret
	keyProv, valueProv := config.MakeProvisionerMetadata(config.ACMEProvisionerName)
	// the secrets of every replica, the one that detached a secret may be gone for good
	filters := svccore.ListFilters{
		{
			Key:        keyProv,
			Value:      valueProv,
			Operator:   svccore.ListFilter_EQUAL,
			IsMetadata: true,
		},
		{
			Key:        mdAttachedKey,
			Value:      "false",
			Operator:   svccore.ListFilter_EQUAL,
			IsMetadata: true,
		},
	}
	for {
		pageCtx, cancelPage := context.WithTimeout(ctx, controllerCallTimeout)
		response, err := o.secretClient.ListSecret(pageCtx, &svcsecret.ListSecretRequest{
			Limit:   int32(limit),
			Offset:  int32(offset),
			Filters: filters,
		})
		cancelPage()
		if err != nil {
			return nil, err
		}
		if len(response.GetSecrets()) == 0 {
			break
		}

		secretsList = append(secretsList, response.GetSecrets()...)
		offset += limit
	}
	return secretsList, nil
}

func (o *CertManager) cleanUpSecrets(ctx context.Context) {
	o.pruneRemovals()
	var g errgroup.Group
	g.SetLimit(10)

	secrets, err := o.listDetachedSecret(ctx)
	if err != nil {
		o.entry.Error().Err(err).Msg("Failed to list detached secrets")
		return
	}
	for _, sec := range secrets {
		g.Go(func() error {
			certUuid := sec.GetMetadata()[mdCertRefKey]
			if certUuid == "" {
				return nil
			}
			// an order in flight would record the certificate again after its record is dropped
			unlock := o.lockCert(certUuid)
			defer unlock()
			readCtx, cancelRead := context.WithTimeout(ctx, controllerCallTimeout)
			defer cancelRead()
			_, err := o.certClient.ReadCertificate(readCtx, &svccore.UuidRequest{
				UUID: certUuid,
			})
			// only a certificate the controller no longer has frees its secret, a failed read proves nothing
			isCertGone := connect.CodeOf(err) == connect.CodeNotFound
			if !isCertGone {
				return nil
			}
			deleteCtx, cancelDelete := context.WithTimeout(ctx, controllerCallTimeout)
			defer cancelDelete()
			_, err = o.secretClient.DeleteSecret(deleteCtx, &svccore.UuidRequest{
				UUID: sec.GetUUID(),
			})
			// another replica may have deleted it first
			isSecretGone := connect.CodeOf(err) == connect.CodeNotFound
			if err != nil && !isSecretGone {
				o.entry.Error().Err(err).Str("uuid", sec.GetUUID()).Msg("Failed to delete secret")
				return nil
			}
			o.dropRecord(certUuid)
			return nil
		})
	}
	_ = g.Wait()
}
