package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
)

type CreateZone struct {
	File string `short:"f" name:"file" help:"Path to a json or yml file containing the zone to create" default:"zone.yml" type:"path" predictor:"file"`

	Init  bool `short:"i" name:"init" help:"Initialize the json file"`
	Force bool `name:"force" help:"Force create zone without confirmation"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *CreateZone) SetClient(client *clients.Client) {
	c.client = client
}

func (c *CreateZone) Run(ctx context.Context) error {
	protoMsg, loaded, err := FileToProto[*zone.Zone](c.File)
	if err != nil {
		return err
	}

	if c.Init {
		err := ProtoSkeletonToFile(&zone.Zone{}, c.File)
		if err != nil {
			return err
		}
		msg.Successf("Initialized json file %s for Zone", msg.Cyan(c.File))
		return nil
	}
	if !loaded {
		return fmt.Errorf("file %s is empty", msg.Cyan(c.File))
	}

	if err := refuseLockAdded(nil, protoMsg.GetMetadata()); err != nil {
		return err
	}

	if err := resolveSecretRefs(ctx, c.client, protoMsg); err != nil {
		return err
	}

	confirm, err := DiffAndConfirm(nil, protoMsg, c.Force)
	if err != nil {
		return err
	}
	if !confirm {
		return nil
	}

	response, err := c.client.Zone().CreateZone(ctx, &svczone.CreateZoneRequest{
		Zone: protoMsg,
	})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response.GetZone())
	}
	msg.Successf("Zone %s successfully from file %s.", msg.Green("created"), msg.Cyan(c.File))

	return nil
}
