package handlers

import (
	"context"

	"github.com/rs/zerolog"
	"github.com/seagalia/flock/agent/starling/envoyctl"
	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/logging"
)

type CertificateHandler struct {
	entry       zerolog.Logger
	confWatcher *envoyctl.ConfigWatcher
}

func NewCertificateHandler(confWatcher *envoyctl.ConfigWatcher) adshandlers.MessageHandler[*cert.Certificate] {
	return &CertificateHandler{
		entry:       logging.Component("certificate_handler"),
		confWatcher: confWatcher,
	}
}

func (h *CertificateHandler) OnMessageAdded(ctx context.Context, message *cert.Certificate) error {
	h.entry.Debug().Msgf("certificate %s added/modified", message.GetUUID())
	if message.GetCertChain() == "" || message.GetPrivateKey() == "" {
		h.entry.Warn().Msgf("certificate %s has empty cert chain or private key", message.GetUUID())
		return nil
	}
	h.confWatcher.AddUpdateCertificate(message)
	return nil
}

func (h *CertificateHandler) OnMessageRemove(ctx context.Context, message *cert.Certificate) error {
	h.entry.Debug().Msgf("certificate %s removed", message.GetUUID())
	h.confWatcher.RemoveCertificate(message.GetUUID())
	return nil
}

func (h *CertificateHandler) Name() string {
	return "certificate"
}
