package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
)

type CreateVs struct {
	File string `short:"f" name:"file" help:"Path to a json or yml file containing the virtual server to create" default:"vs.yml" type:"path" predictor:"file"`

	Init  bool `short:"i" name:"init" help:"Initialize the json file"`
	Force bool `name:"force" help:"Force create virtual server without confirmation"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *CreateVs) SetClient(client *clients.Client) {
	c.client = client
}

func (c *CreateVs) Run(ctx context.Context) error {
	protoMsg, loaded, err := FileToProto[*vs.VirtualServer](c.File)
	if err != nil {
		return err
	}
	protoMsg.UUID = ""

	if c.Init {
		err := ProtoSkeletonToFile(&vs.VirtualServer{}, c.File)
		if err != nil {
			return err
		}
		msg.Successf("Initialized json file %s for virtual server", msg.Cyan(c.File))
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

	response, err := c.client.VirtualServer().CreateVS(ctx, &svcvs.CreateVSRequest{
		Vs: protoMsg,
	})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response.GetVs())
	}
	msg.Successf("Virtual Server %s successfully from file %s.", msg.Green("created"), msg.Cyan(c.File))

	return nil
}
