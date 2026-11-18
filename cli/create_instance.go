package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
)

type CreateInstance struct {
	File  string `short:"f" name:"file" help:"Path to a json or yml file containing the instance to create" default:"instance.yml" type:"path" predictor:"file"`
	Init  bool   `short:"i" name:"init" help:"Initialize the json file"`
	Force bool   `name:"force" help:"Force create instance without confirmation"`

	Json   bool `short:"j" name:"json" help:"Format in json instead of human table readable."`
	client *clients.Client
}

func (c *CreateInstance) SetClient(client *clients.Client) {
	c.client = client
}

func (c *CreateInstance) Run(ctx context.Context) error {
	protoMsg, loaded, err := FileToProto[*instance.Instance](c.File)
	if err != nil {
		return err
	}
	protoMsg.UUID = ""

	if c.Init {
		err := ProtoSkeletonToFile(&instance.Instance{}, c.File)
		if err != nil {
			return err
		}
		msg.Successf("Initialized json file %s for instance", msg.Cyan(c.File))
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

	response, err := c.client.Instance().CreateInstance(ctx, &svcinstance.CreateInstanceRequest{
		Instance: protoMsg,
	})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response.GetInstance())
	}
	msg.Successf("Instance %s successfully from file %s.", msg.Green("created"), msg.Cyan(c.File))

	return nil
}
