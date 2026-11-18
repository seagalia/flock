package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
)

type UpdateVs struct {
	File string `short:"f" name:"file" help:"Path to a json or yml file containing the virtual server to update" default:"vs.yml" type:"path" predictor:"file"`

	Named VsNamed `arg:"" name:"virtual-server" help:"virtual server name or uuid" predictor:"vs"`
	Force bool    `name:"force" help:"Force update virtual server without confirmation"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *UpdateVs) SetClient(client *clients.Client) {
	c.client = client
}

func (c *UpdateVs) Run(ctx context.Context) error {
	current, err := FindVirtServ(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLocked("vs", current.GetName(), current.GetMetadata()); err != nil {
		return err
	}

	protoMsg, loaded, err := FileToProto[*vs.VirtualServer](c.File)
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

	response, err := c.client.VirtualServer().UpdateVS(ctx, &svcvs.UpdateVSRequest{
		UUID: protoMsg.UUID,
		Vs:   protoMsg,
	})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response.GetVs())
	}

	msg.Successf("Virtual Server %s updated from file %s .", msg.Cyan(c.Named.Identifier()), msg.Cyan(c.File))
	return nil
}
