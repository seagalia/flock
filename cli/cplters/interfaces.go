package cplters

import (
	"context"

	"github.com/seagalia/flock/sdk/clients"
)

type Completer interface {
	ToMap() (map[string]any, error)
	Complete(ctx context.Context, client *clients.Client) error
}
