package cli

import (
	"context"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type AllowAllReadPermission struct {
	ResourceType string `short:"r" name:"resource-type" help:"Resource type" required:"" enum:"instance,backend,vip,vs,certificate,zone,secret"`

	Named NameUuid `arg:"" name:"resource" help:"resource name or uuid" predictor:"by-resource-type"`

	AllowAllRead bool `short:"a" name:"allow-all-read" help:"Allow all read"`

	client *clients.Client
}

func (c *AllowAllReadPermission) SetClient(client *clients.Client) {
	c.client = client
}

func (c *AllowAllReadPermission) Run(ctx context.Context) error {
	resType := TypeFromShort(c.ResourceType)

	uuid := c.Named.Identifier()
	if !c.Named.IsUUID() {
		res, err := FindFirstTypedByName(ctx, c.client, resType, c.Named.Identifier())
		if err != nil {
			return err
		}
		uuid = helpers.GetIdentifier(res)
	}

	allowAllReadPermRequest := &svcperm.SetAllowAllReadRequest{
		ResourceType: resType,
		ResourceUuid: uuid,
		AllowAllRead: c.AllowAllRead,
	}

	_, err := c.client.Permission().SetAllowAllRead(ctx, allowAllReadPermRequest)
	if err != nil {
		return err
	}

	msg.Successf("Allow all read permission successfully set to %s",
		msg.Cyan(c.Named.Identifier()),
	)
	return nil
}
