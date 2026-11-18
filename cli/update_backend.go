package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
)

type UpdateBackend struct {
	File string `short:"f" name:"file" help:"Path to a json or yml file containing the backend to update" default:"backend.yml" type:"path" predictor:"file"`

	Named BackendNamed `arg:"" name:"backend" help:"backend name or uuid" predictor:"backend"`
	Force bool         `name:"force" help:"Force update backend without confirmation"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *UpdateBackend) SetClient(client *clients.Client) {
	c.client = client
}

func (c *UpdateBackend) Run(ctx context.Context) error {
	current, err := FindBackend(ctx, c.client, &c.Named)
	if err != nil {
		return err
	}

	if err := refuseLocked("backend", current.GetName(), current.GetMetadata()); err != nil {
		return err
	}

	protoMsg, loaded, err := FileToProto[*backend.Backend](c.File)
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

	response, err := c.client.Backend().UpdateBackend(ctx, &svcbackend.UpdateBackendRequest{
		UUID:    protoMsg.UUID,
		Backend: protoMsg,
	})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response.GetBackend())
	}
	msg.Successf("Backend %s updated from file %s .", msg.Cyan(c.Named.Identifier()), msg.Cyan(c.File))

	return nil
}
