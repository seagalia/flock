package cli

import (
	"context"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	core "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

type DeleteVs struct {
	Named       VsNamed `arg:"" name:"virtual-server" help:"virtual server name or uuid" predictor:"vs"`
	Force       bool    `name:"force" help:"Force delete virtual server without confirmation"`
	ForceLocked bool    `name:"force-locked" help:"Delete it even when a provisioner manages it (flock.autoprovision), admin only"`

	client *clients.Client
}

func (c *DeleteVs) SetClient(client *clients.Client) {
	c.client = client
}

func (c *DeleteVs) Run(ctx context.Context) error {
	current, err := FindVirtServ(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLockedDelete(ctx, c.client, "vs", current.GetName(), current.GetMetadata(), c.ForceLocked); err != nil {
		return err
	}

	confirm, err := DiffAndConfirm(current, nil, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	_, err = c.client.VirtualServer().DeleteVS(ctx, &core.UuidRequest{UUID: current.GetUUID()})
	if err != nil {
		return err
	}
	msg.Successf("Virtual Server successfully %s.", msg.Red("deleted"))
	return nil
}
