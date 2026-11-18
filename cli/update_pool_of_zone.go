package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
)

type UpdatePoolOfZone struct {
	File string `short:"f" name:"file" help:"Path to a json or yml file containing the pool to update, matched by its name" default:"pool.yml" type:"path" predictor:"file"`

	Named ZoneNamed `arg:"" name:"zone" help:"zone name or uuid" predictor:"zone"`
	Force bool      `name:"force" help:"Force update the pool without confirmation"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *UpdatePoolOfZone) SetClient(client *clients.Client) {
	c.client = client
}

func (c *UpdatePoolOfZone) Run(ctx context.Context) error {
	current, err := FindZone(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLocked("zone", current.GetName(), current.GetMetadata()); err != nil {
		return err
	}

	pool, loaded, err := FileToProto[*zone.IpPool](c.File)
	if err != nil {
		return err
	}
	if !loaded {
		return fmt.Errorf("file %s is empty", msg.Cyan(c.File))
	}

	currentPool, err := FindPoolOfZone(current, pool.GetName())
	if err != nil {
		return err
	}

	if err := refuseLocked("pool", currentPool.GetName(), currentPool.GetMetadata()); err != nil {
		return err
	}
	if err := refuseLockAdded(currentPool.GetMetadata(), pool.GetMetadata()); err != nil {
		return err
	}

	confirm, err := DiffAndConfirm(currentPool, pool, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	response, err := c.client.Zone().UpdatePoolOfZone(ctx, &svczone.UpdatePoolOfZoneRequest{
		UUID: current.GetUUID(),
		Pool: pool,
	})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response.GetZone())
	}
	msg.Successf("Pool %s of zone %s updated from file %s.", msg.Cyan(pool.GetName()), msg.Cyan(c.Named.Identifier()), msg.Cyan(c.File))
	return nil
}
