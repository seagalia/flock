package cli

import (
	"context"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	core "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

type DeleteInstance struct {
	Named       InstanceNamed `arg:"" name:"instance" help:"instance name or uuid" predictor:"instance"`
	Force       bool          `name:"force" help:"Force delete instance without confirmation"`
	ForceLocked bool          `name:"force-locked" help:"Delete it even when a provisioner manages it (flock.autoprovision), admin only"`

	client *clients.Client
}

func (c *DeleteInstance) SetClient(client *clients.Client) {
	c.client = client
}

func (c *DeleteInstance) Run(ctx context.Context) error {
	current, err := FindInstance(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLockedDelete(ctx, c.client, "instance", current.GetName(), current.GetMetadata(), c.ForceLocked); err != nil {
		return err
	}

	confirm, err := DiffAndConfirm(current, nil, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	_, err = c.client.Instance().DeleteInstance(ctx, &core.UuidRequest{UUID: current.GetUUID()})
	if err != nil {
		return err
	}
	msg.Successf("Instance successfully %s.", msg.Red("deleted"))
	return nil
}
