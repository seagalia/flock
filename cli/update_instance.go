package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
)

type UpdateInstance struct {
	File string `short:"f" name:"file" help:"Path to a json or yml file containing the instance to update" default:"instance.yml" type:"path" predictor:"file"`

	Named InstanceNamed `arg:"" name:"instance" help:"instance name or uuid" predictor:"instance"`
	Force bool          `name:"force" help:"Force update instance without confirmation"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *UpdateInstance) SetClient(client *clients.Client) {
	c.client = client
}

func (c *UpdateInstance) Run(ctx context.Context) error {
	current, err := FindInstance(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLocked("instance", current.GetName(), current.GetMetadata()); err != nil {
		return err
	}

	protoMsg, loaded, err := FileToProto[*instance.Instance](c.File)
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

	response, err := c.client.Instance().UpdateInstance(ctx, &svcinstance.UpdateInstanceRequest{
		UUID:     protoMsg.UUID,
		Instance: protoMsg,
	})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response.GetInstance())
	}

	msg.Successf("Instance %s updated from file %s .", msg.Cyan(c.Named.Identifier()), msg.Cyan(c.File))

	return nil
}
