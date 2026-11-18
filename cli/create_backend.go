package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
)

type CreateBackend struct {
	File string `short:"f" name:"file" help:"Path to a json or yml file containing the backend to create" default:"backend.yml" type:"path" predictor:"file"`

	Init  bool `short:"i" name:"init" help:"Initialize the json file"`
	Json  bool `short:"j" name:"json" help:"Format in json instead of human table readable."`
	Force bool `name:"force" help:"Force create backend without confirmation"`

	client *clients.Client
}

func (c *CreateBackend) SetClient(client *clients.Client) {
	c.client = client
}

func (c *CreateBackend) Run(ctx context.Context) error {
	protoMsg, loaded, err := FileToProto[*backend.Backend](c.File)
	if err != nil {
		return err
	}
	protoMsg.UUID = ""

	if c.Init {
		err := ProtoSkeletonToFile(&backend.Backend{}, c.File)
		if err != nil {
			return err
		}
		msg.Successf("Initialized json file %s for backend", msg.Cyan(c.File))
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

	response, err := c.client.Backend().CreateBackend(ctx, &svcbackend.CreateBackendRequest{
		Backend: protoMsg,
	})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response.GetBackend())
	}
	msg.Successf("Backend %s successfully from file %s.", msg.Green("created"), msg.Cyan(c.File))
	return nil
}
