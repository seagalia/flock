package cli

import (
	"context"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	core "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

type DeleteVip struct {
	Named       VipNamed `arg:"" name:"vip" help:"vip name or uuid" predictor:"vip"`
	Force       bool     `name:"force" help:"Force delete vip without confirmation"`
	ForceLocked bool     `name:"force-locked" help:"Delete it even when a provisioner manages it (flock.autoprovision), admin only"`

	client *clients.Client
}

func (c *DeleteVip) SetClient(client *clients.Client) {
	c.client = client
}

func (c *DeleteVip) Run(ctx context.Context) error {
	current, err := FindVIP(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLockedDelete(ctx, c.client, "vip", current.GetName(), current.GetMetadata(), c.ForceLocked); err != nil {
		return err
	}

	confirm, err := DiffAndConfirm(current, nil, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	_, err = c.client.Vip().DeleteVIP(ctx, &core.UuidRequest{UUID: current.GetUUID()})
	if err != nil {
		return err
	}
	msg.Successf("VIP successfully %s.", msg.Red("deleted"))
	return nil
}
