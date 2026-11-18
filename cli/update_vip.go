package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
)

type UpdateVip struct {
	File string `short:"f" name:"file" help:"Path to a json or yml file containing the vip to create" default:"vip.yml" type:"path" predictor:"file"`

	Named VipNamed `arg:"" name:"vip" help:"vip name or uuid" predictor:"vip"`
	Force bool     `name:"force" help:"Force update vip without confirmation"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *UpdateVip) SetClient(client *clients.Client) {
	c.client = client
}

func (c *UpdateVip) Run(ctx context.Context) error {
	current, err := FindVIP(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLocked("vip", current.GetName(), current.GetMetadata()); err != nil {
		return err
	}

	protoMsg, loaded, err := FileToProto[*vip.VIP](c.File)
	if err != nil {
		return err
	}

	if !loaded {
		return fmt.Errorf("file %s is empty", msg.Cyan(c.File))
	}

	if err := refuseLockAdded(current.GetMetadata(), protoMsg.GetMetadata()); err != nil {
		return err
	}

	if err := resolveSecretRefs(ctx, c.client, protoMsg); err != nil {
		return err
	}

	protoMsg.UUID = current.GetUUID()
	confirm, err := DiffAndConfirm(current, protoMsg, c.Force)
	if err != nil {
		return err
	}

	if !confirm {
		return nil
	}

	response, err := c.client.Vip().UpdateVIP(ctx, &svcvip.UpdateVIPRequest{
		UUID: protoMsg.UUID,
		Vip:  protoMsg,
	})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response.GetVip())
	}

	msg.Successf("VIP %s updated from file %s .", msg.Cyan(c.Named.Identifier()), msg.Cyan(c.File))
	return nil
}
