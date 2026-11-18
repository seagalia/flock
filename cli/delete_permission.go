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

type DeletePermission struct {
	ResourceType string   `short:"r" name:"resource-type" help:"Resource type" required:"" enum:"instance,backend,vip,vs,certificate,zone,secret"`
	Named        NameUuid `arg:"" name:"resource" help:"resource name or uuid" predictor:"by-resource-type"`

	MemberType string `short:"t" name:"member-type" help:"Member type" required:"" enum:"user,group"`
	MemberName string `short:"n" name:"member-name" help:"Member name" required:""`

	client *clients.Client
}

func (c *DeletePermission) SetClient(client *clients.Client) {
	c.client = client
}

func (c *DeletePermission) Run(ctx context.Context) error {
	resType := TypeFromShort(c.ResourceType)

	uuid := c.Named.Identifier()
	if !c.Named.IsUUID() {
		res, err := FindFirstTypedByName(ctx, c.client, resType, c.Named.Identifier())
		if err != nil {
			return err
		}
		uuid = helpers.GetIdentifier(res)
	}

	member := &permission.Member{
		MemberType: permission.MemberType(permission.MemberType_value[strings.ToUpper(c.MemberType)]),
		MemberName: c.MemberName,
	}

	deletePermRequest := &svcperm.DeletePermissionRequest{
		ResourceType: resType,
		ResourceUuid: uuid,
		Member:       member,
	}

	_, err := c.client.Permission().DeletePermission(ctx, deletePermRequest)
	if err != nil {
		return err
	}

	msg.Successf("Permission successfully %s.", msg.Red("deleted"))
	return nil
}

func (c *DeletePermission) Help() string {
	return `Delete a permission to a member on a resource. 
The member can be a user or a group.
Member role will be removed in all sub resources in the resource.`
}
