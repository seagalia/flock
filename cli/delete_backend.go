package cli

import (
	"context"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	core "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

type DeleteBackend struct {
	Named       BackendNamed `arg:"" name:"backend" help:"backend name or uuid" predictor:"backend"`
	Force       bool         `name:"force" help:"Force delete backend without confirmation"`
	ForceLocked bool         `name:"force-locked" help:"Delete it even when a provisioner manages it (flock.autoprovision), admin only"`

	client *clients.Client
}

func (c *DeleteBackend) SetClient(client *clients.Client) {
	c.client = client
}

func (c *DeleteBackend) Run(ctx context.Context) error {
	current, err := FindBackend(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLockedDelete(ctx, c.client, "backend", current.GetName(), current.GetMetadata(), c.ForceLocked); err != nil {
		return err
	}

	confirm, err := DiffAndConfirm(current, nil, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	_, err = c.client.Backend().DeleteBackend(ctx, &core.UuidRequest{UUID: current.GetUUID()})
	if err != nil {
		return err
	}
	msg.Successf("Backend successfully %s.", msg.Red("deleted"))

	return nil
}
