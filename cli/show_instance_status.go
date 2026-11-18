package cli

import (
	"context"
	"os"

	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

type ShowInstanceStatus struct {
	Named InstanceNamed `arg:"" name:"instance" help:"instance name or uuid" predictor:"instance"`

	Json bool `short:"j" name:"json" help:"Print the instance in JSON format"`

	client *clients.Client
}

func (c *ShowInstanceStatus) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ShowInstanceStatus) Run(ctx context.Context) error {
	uuid := c.Named.Identifier()
	if !c.Named.IsUUID() {
		res, err := FindFirstByName[*instance.Instance](ctx, c.client, c.Named.Identifier())
		if err != nil {
			return err
		}
		uuid = res.UUID
	}

	response, err := c.client.Instance().GetInstanceStatus(ctx, &svccore.UuidRequest{UUID: uuid})
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response)
	}

	table := MakeTableWriter([]string{"Current State", "Desired State", "Error"}, os.Stdout)
	table.Options(tableBordered())
	if err := table.Append([]string{
		response.GetStatus().GetCurrentState().String(),
		response.GetStatus().GetDesiredState().String(),
		response.GetStatus().GetErrorMessage(),
	}); err != nil {
		return err
	}
	return table.Render()
}
