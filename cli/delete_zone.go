package cli

import (
	"context"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	core "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

type DeleteZone struct {
	Named       ZoneNamed `arg:"" name:"zone" help:"zone name" predictor:"zone"`
	Force       bool      `name:"force" help:"Force delete zone without confirmation"`
	ForceLocked bool      `name:"force-locked" help:"Delete it even when a provisioner manages it (flock.autoprovision), admin only"`
	client      *clients.Client
}

func (c *DeleteZone) SetClient(client *clients.Client) {
	c.client = client
}

func (c *DeleteZone) Run(ctx context.Context) error {
	current, err := FindZone(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLockedDelete(ctx, c.client, "zone", current.GetName(), current.GetMetadata(), c.ForceLocked); err != nil {
		return err
	}

	confirm, err := DiffAndConfirm(current, nil, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	_, err = c.client.Zone().DeleteZone(ctx, &core.UuidRequest{UUID: current.GetUUID()})
	if err != nil {
		return err
	}
	msg.Successf("Zone successfully %s.", msg.Red("deleted"))
	return nil
}
