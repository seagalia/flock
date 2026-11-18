package challenges

import (
	"context"

	"github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ adshandlers.MessageHandler[*secret.Secret] = (*SecretHandler)(nil)

// SecretHandler feeds a Table from the challenge secrets the controller delivers.
type SecretHandler struct {
	table *Table
}

func NewSecretHandler(table *Table) *SecretHandler {
	return &SecretHandler{table: table}
}

func (h *SecretHandler) OnMessageAdded(_ context.Context, aSecret *secret.Secret) error {
	if !isChallengeSecret(aSecret) {
		h.table.Drop(aSecret.GetUUID())
		return nil
	}
	h.table.Replace(aSecret.GetUUID(), DecodeData(aSecret.GetData()))
	return nil
}

func (h *SecretHandler) OnMessageRemove(_ context.Context, aSecret *secret.Secret) error {
	h.table.Drop(aSecret.GetUUID())
	return nil
}

func (h *SecretHandler) Name() string {
	return "acme_challenges"
}

// isChallengeSecret also asks for the metadata the provisioner sets on its challenge secrets, so a secret a user marks
// as one can't make the replicas answer the challenges it holds.
func isChallengeSecret(aSecret *secret.Secret) bool {
	md := aSecret.GetMetadata()
	provisionerKey, provisionerValue := config.MakeProvisionerMetadata(config.ACMEProvisionerName)
	hasChallengeKey := md[MetadataKey] == "true"
	isLocked := md[helpers.MetadataLockKey] == "true"
	isFromACME := md[provisionerKey] == provisionerValue
	return hasChallengeKey && isLocked && isFromACME
}
