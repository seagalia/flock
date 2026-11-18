package adshandlers

import (
	"context"
	"sync"

	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/logging"
)

// SecretRefresher runs again the resources pointing at a secret when the secret is created or updated. A removed
// secret runs nothing, the handlers keep the values they last got.
type SecretRefresher struct {
	rt *runtime.Runtime
}

// NewSecretRefresher runs again the resources of the handlers registered in rt.
func NewSecretRefresher(rt *runtime.Runtime) *SecretRefresher {
	return &SecretRefresher{rt: rt}
}

func (r *SecretRefresher) OnMessageAdded(ctx context.Context, aSecret *secret.Secret) error {
	entry := logging.Component("secret-refresher")
	wg := &sync.WaitGroup{}
	for _, linked := range r.rt.SecretLinks.LinkedTo(aSecret.GetUUID()) {
		for _, handler := range r.rt.Handlers(linked.TypeUrl) {
			wg.Go(func() {
				err := rerunWithTimeout(ctx, handler, linked.UUID)
				if err != nil {
					entry.Error().Str("type", linked.TypeUrl).Str("uuid", linked.UUID).Err(err).
						Msg("Failed to apply again a resource pointing at a secret")
				}
			})
		}
	}
	wg.Wait()
	return nil
}

func rerunWithTimeout(ctx context.Context, handler runtime.AdsHandler, uuid string) error {
	ctx, cancel := context.WithTimeout(ctx, resourceTimeout)
	defer cancel()
	return handler.Rerun(ctx, uuid)
}

func (r *SecretRefresher) OnMessageRemove(context.Context, *secret.Secret) error {
	return nil
}

func (r *SecretRefresher) Name() string {
	return "secret_refresher"
}
