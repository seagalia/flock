package handlers

import (
	"context"
	"github.com/seagalia/flock/agent/starling/backendctl"
	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
)

type BackendHandler struct {
	jointer backendctl.Jointer
}

func NewBackendHandler(jointer backendctl.Jointer) adshandlers.MessageHandler[*backend.Backend] {
	return &BackendHandler{
		jointer: jointer,
	}
}

func (h *BackendHandler) OnMessageAdded(ctx context.Context, message *backend.Backend) error {
	return h.jointer.OnMessageAdded(ctx, message)
}

func (h *BackendHandler) OnMessageRemove(ctx context.Context, message *backend.Backend) error {
	return h.jointer.OnMessageRemove(ctx, message)
}

func (h *BackendHandler) Stop() error {
	if cleanable, ok := h.jointer.(adshandlers.Stoppable); ok {
		return cleanable.Stop()
	}
	return nil
}

func (h *BackendHandler) Name() string {
	return "backend"
}
