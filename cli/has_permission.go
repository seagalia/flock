package cli

import (
	"context"
	"os"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type HasPermission struct {
	ResourceType string `short:"r" name:"resource-type" help:"Resource type" required:"" enum:"instance,backend,vip,vs,certificate,zone,secret"`

	Named NameUuid `arg:"" name:"resource" help:"resource name or uuid" predictor:"by-resource-type"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	Role       string `short:"l" name:"role" help:"Role" enum:",reader,writer,owner" default:""`
	MemberType string `short:"t" name:"member-type" help:"Member type" required:"" enum:"user,group"`
	MemberName string `short:"n" name:"member-name" help:"Member name" required:""`

	client *clients.Client
}

func (c *HasPermission) SetClient(client *clients.Client) {
	c.client = client
}

func (c *HasPermission) Run(ctx context.Context) error {
	resType := TypeFromShort(c.ResourceType)

	uuid := c.Named.Identifier()
	if !c.Named.IsUUID() {
		res, err := FindFirstTypedByName(ctx, c.client, resType, c.Named.Identifier())
		if err != nil {
			return err
		}
		uuid = helpers.GetIdentifier(res)
	}

	role := permission.Role(permission.Role_value[strings.ToUpper(c.Role)])
	member := &permission.Member{
		MemberType: permission.MemberType(permission.MemberType_value[strings.ToUpper(c.MemberType)]),
		MemberName: c.MemberName,
	}

	hasPermRequest := &svcperm.HasPermissionRequest{
		ResourceType: resType,
		ResourceUuid: uuid,
		Role:         role,
		Member:       member,
	}

	response, err := c.client.Permission().HasPermission(ctx, hasPermRequest)
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response)
	}

	if response.GetHasPermission() {
		msg.Successf("Member %s has permission %s on %s.", msg.Yellow(c.MemberName), msg.Underline(c.Role), msg.Cyan(c.Named.Identifier()))
	} else {
		msg.Errorf("Member %s has no permission %s on %s.", msg.Yellow(c.MemberName), msg.Underline(c.Role), msg.Cyan(c.Named.Identifier()))
		os.Exit(1)
	}

	return nil
}
