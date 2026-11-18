package cli

import (
	"context"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
)

type DeletePoolFromZone struct {
	Named ZoneNamed `arg:"" name:"zone" help:"zone name or uuid" predictor:"zone"`
	Pool  string    `arg:"" name:"pool" help:"name of the pool to delete" predictor:"zone-pool"`
	Force bool      `name:"force" help:"Force delete the pool without confirmation"`

	client *clients.Client
}

func (c *DeletePoolFromZone) SetClient(client *clients.Client) {
	c.client = client
}

func (c *DeletePoolFromZone) Run(ctx context.Context) error {
	current, err := FindZone(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLocked("zone", current.GetName(), current.GetMetadata()); err != nil {
		return err
	}

	pool, err := FindPoolOfZone(current, c.Pool)
	if err != nil {
		return err
	}

	if err := refuseLocked("pool", pool.GetName(), pool.GetMetadata()); err != nil {
		return err
	}

	confirm, err := DiffAndConfirm(pool, nil, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	_, err = c.client.Zone().DeletePoolFromZone(ctx, &svczone.DeletePoolFromZoneRequest{
		UUID:     current.GetUUID(),
		PoolName: c.Pool,
	})
	if err != nil {
		return err
	}
	msg.Successf("Pool %s successfully %s.", msg.Cyan(c.Pool), msg.Red("deleted"))
	return nil
}
