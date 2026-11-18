package discoverers

import (
	"context"
	"fmt"
	"time"

	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type Refresh struct {
	backendDef *backend.Backend
	next       Discoverer
	stateStore *stores.StateStore
}

func NewRefresh(backendDef *backend.Backend, next Discoverer, stateStore *stores.StateStore) *Refresh {
	return &Refresh{
		backendDef: backendDef,
		next:       next,
		stateStore: stateStore,
	}
}

func (d *Refresh) Run(ctx context.Context, found chan<- []*core.IPAddr) error {
	refreshInterval := d.backendDef.GetRefreshRate().AsDuration()
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	// only the first discovery marks the backend adding: a later refresh keeps the state of the previous one, an error
	// included, until its own result replaces it
	d.stateStore.SetState(d.backendDef, agentstatus.StatusResponse_ADDING)
	for {
		err := d.next.Run(ctx, found)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			// a failed refresh keeps the last endpoints and is retried at the next tick, since the failure of a dns
			// server or an eds may be transient and nothing else would restart the discovery
			d.stateStore.AddError(d.backendDef, fmt.Errorf("unable to refresh endpoints: %w", err))
		default:
			d.stateStore.ClearState(helpers.TypeUrl[*backend.Backend](), d.backendDef.GetUUID())
		}
		ticker.Reset(refreshInterval)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
