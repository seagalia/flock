package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
)

type UpdateZone struct {
	File string `short:"f" name:"file" help:"Path to a json or yml file containing the zone to update" default:"zone.yml" type:"path" predictor:"file"`

	Named ZoneNamed `arg:"" name:"zone" help:"zone name" predictor:"zone"`
	Force bool      `name:"force" help:"Force update zone without confirmation"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *UpdateZone) SetClient(client *clients.Client) {
	c.client = client
}

func (c *UpdateZone) Run(ctx context.Context) error {
	current, err := FindZone(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLocked("zone", current.GetName(), current.GetMetadata()); err != nil {
		return err
	}

	protoMsg, loaded, err := FileToProto[*zone.Zone](c.File)
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

	response, err := c.client.Zone().UpdateZone(ctx, &svczone.UpdateZoneRequest{
		UUID: protoMsg.GetUUID(),
		Zone: protoMsg,
	})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response.GetZone())
	}

	msg.Successf("Zone %s updated from file %s .", msg.Cyan(c.Named.Identifier()), msg.Cyan(c.File))
	return nil
}
