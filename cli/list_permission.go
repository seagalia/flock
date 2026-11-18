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

type ListPermission struct {
	ResourceType string `short:"r" name:"resource-type" help:"Resource type" required:"" enum:"instance,backend,vip,vs,certificate,zone,secret"`

	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	Named NameUuid `arg:"" name:"resource" help:"resource name or uuid" predictor:"by-resource-type"`

	Role string `short:"l" name:"role" help:"Role" enum:",reader,writer,owner" default:""`

	client *clients.Client
}

func (c *ListPermission) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ListPermission) Run(ctx context.Context) error {
	resType := TypeFromShort(c.ResourceType)
	uuid := c.Named.Identifier()
	if !c.Named.IsUUID() {
		res, err := FindFirstTypedByName(ctx, c.client, resType, c.Named.Identifier())
		if err != nil {
			return err
		}
		uuid = helpers.GetIdentifier(res)
	}

	var listPermRequest *svcperm.ListMemberPermissionsRequest
	if c.Role != "" {
		role := permission.Role(permission.Role_value[strings.ToUpper(c.Role)])
		listPermRequest = &svcperm.ListMemberPermissionsRequest{
			ResourceType: resType,
			ResourceUuid: uuid,
			Role:         role,
		}
	} else {
		listPermRequest = &svcperm.ListMemberPermissionsRequest{
			ResourceType: resType,
			ResourceUuid: uuid,
			SeeAll:       true,
		}
	}

	response, err := c.client.Permission().ListMemberPermissions(ctx, listPermRequest)
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoListJson[*permission.MemberPermission](response.GetMemberPermissions())
	}

	if len(response.GetMemberPermissions()) == 0 {
		msg.Info("No permissions found.")
		return nil
	}

	table := MakeTableWriter([]string{"Member Type", "Member Name", "Role"})
	for _, perm := range response.GetMemberPermissions() {
		if err := table.Append([]string{
			msg.Yellow(perm.GetMember().GetMemberType().String()).String(),
			msg.Cyan(perm.GetMember().GetMemberName()).String(),
			msg.Underline(perm.GetRole().String()).String(),
		}); err != nil {
			return err
		}
	}
	return table.Render()
}
