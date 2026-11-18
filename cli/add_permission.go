package cli

import (
	"context"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type AddPermission struct {
	ResourceType string `short:"r" name:"resource-type" help:"Resource type" required:"" enum:"instance,backend,vip,vs,certificate,zone,secret"`

	Named NameUuid `arg:"" name:"resource" help:"resource name or uuid" predictor:"by-resource-type"`

	Role       string `short:"l" name:"role" help:"Role" enum:",reader,writer,owner" default:""`
	MemberType string `short:"t" name:"member-type" help:"Member type" required:"" enum:"user,group"`
	MemberName string `short:"n" name:"member-name" help:"Member name" required:""`

	client *clients.Client
}

func (c *AddPermission) SetClient(client *clients.Client) {
	c.client = client
}

func (c *AddPermission) Run(ctx context.Context) error {
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

	addPermRequest := &svcperm.AddPermissionRequest{
		ResourceType: resType,
		ResourceUuid: uuid,
		Role:         role,
		Member:       member,
	}

	_, err := c.client.Permission().AddPermission(ctx, addPermRequest)
	if err != nil {
		return err
	}
	msg.Successf("Permission %s added to %s on %s",
		msg.Underline(role), msg.Yellow(member), msg.Cyan(c.Named.Identifier()),
	)
	return nil
}

func (c *AddPermission) Help() string {
	return `Add a permission to a member on a resource. 
The member can be a user or a group.
Member will have this role set on all sub resources in the resource.`
}
