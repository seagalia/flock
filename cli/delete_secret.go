package cli

import (
	"context"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	core "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

type DeleteSecret struct {
	Named       SecretNamed `arg:"" name:"secret" help:"secret name or uuid" predictor:"secret"`
	Force       bool        `name:"force" help:"Force delete secret without confirmation"`
	ForceLocked bool        `name:"force-locked" help:"Delete it even when a provisioner manages it (flock.autoprovision), admin only"`

	client *clients.Client
}

func (c *DeleteSecret) SetClient(client *clients.Client) {
	c.client = client
}

func (c *DeleteSecret) Run(ctx context.Context) error {
	current, err := FindSecret(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLockedDelete(ctx, c.client, "secret", current.GetName(), current.GetMetadata(), c.ForceLocked); err != nil {
		return err
	}

	confirm, err := DiffAndConfirm(withoutSecretValues(current), nil, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	_, err = c.client.Secret().DeleteSecret(ctx, &core.UuidRequest{UUID: current.GetUUID()})
	if err != nil {
		return err
	}
	msg.Successf("Secret successfully %s.", msg.Red("deleted"))

	return nil
}
