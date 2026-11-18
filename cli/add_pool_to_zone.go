package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
)

type AddPoolToZone struct {
	File string `short:"f" name:"file" help:"Path to a json or yml file containing the pool to add" default:"pool.yml" type:"path" predictor:"file"`

	Named ZoneNamed `arg:"" name:"zone" help:"zone name or uuid" predictor:"zone"`
	Force bool      `name:"force" help:"Force add the pool without confirmation"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *AddPoolToZone) SetClient(client *clients.Client) {
	c.client = client
}

func (c *AddPoolToZone) Run(ctx context.Context) error {
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

	if err := refuseLockAdded(nil, pool.GetMetadata()); err != nil {
		return err
	}

	confirm, err := DiffAndConfirm(nil, pool, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	response, err := c.client.Zone().AddPoolToZone(ctx, &svczone.AddPoolToZoneRequest{
		UUID: current.GetUUID(),
		Pool: pool,
	})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response.GetZone())
	}
	msg.Successf("Pool %s %s to zone %s.", msg.Cyan(pool.GetName()), msg.Green("added"), msg.Cyan(c.Named.Identifier()))
	return nil
}
