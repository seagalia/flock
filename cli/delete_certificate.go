package cli

import (
	"context"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	core "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

type DeleteCertificate struct {
	Named       CertNamed `arg:"" name:"certificate" help:"certificate name or uuid" predictor:"certificate"`
	Force       bool      `name:"force" help:"Force delete backend without confirmation"`
	ForceLocked bool      `name:"force-locked" help:"Delete it even when a provisioner manages it (flock.autoprovision), admin only"`

	client *clients.Client
}

func (c *DeleteCertificate) SetClient(client *clients.Client) {
	c.client = client
}

func (c *DeleteCertificate) Run(ctx context.Context) error {
	current, err := FindCertificate(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLockedDelete(ctx, c.client, "certificate", current.GetName(), current.GetMetadata(), c.ForceLocked); err != nil {
		return err
	}

	confirm, err := DiffAndConfirm(current, nil, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	_, err = c.client.Certificate().DeleteCertificate(ctx, &core.UuidRequest{UUID: current.GetUUID()})
	if err != nil {
		return err
	}
	msg.Successf("Certificate successfully %s.", msg.Red("deleted"))

	return nil
}
