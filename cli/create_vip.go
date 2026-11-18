package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
)

type CreateVip struct {
	File string `short:"f" name:"file" help:"Path to a json or yml file containing the vip to create" default:"vip.yml" type:"path" predictor:"file"`

	Init  bool `short:"i" name:"init" help:"Initialize the json file"`
	Force bool `name:"force" help:"Force create vip without confirmation"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	client *clients.Client
}

func (c *CreateVip) SetClient(client *clients.Client) {
	c.client = client
}

func (c *CreateVip) Run(ctx context.Context) error {
	protoMsg, loaded, err := FileToProto[*vip.VIP](c.File)
	if err != nil {
		return err
	}
	protoMsg.UUID = ""

	if c.Init {
		err := ProtoSkeletonToFile(&vip.VIP{}, c.File)
		if err != nil {
			return err
		}
		msg.Successf("Initialized json file %s for VIP", msg.Cyan(c.File))
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

	response, err := c.client.Vip().CreateVIP(ctx, &svcvip.CreateVIPRequest{
		Vip: protoMsg,
	})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response.GetVip())
	}
	msg.Successf("VIP %s successfully from file %s.", msg.Green("created"), msg.Cyan(c.File))

	return nil
}
