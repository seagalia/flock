package cli

import (
	"context"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
)

type ListInstance struct {
	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	ShowUuid bool `short:"u" name:"show-uuid" help:"Show the complete UUID"`

	Filters []string `short:"f" name:"filter" sep:"none" help:"Filter by metadata, example foo==bar will find all which have key foo and value bar. Other comparison are == (equality), != (different), ~= (LIKE) and ~! (NOT LIKE). This can be set multiple times."`

	client *clients.Client
}

func (c *ListInstance) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ListInstance) Run(ctx context.Context) error {
	var offset = 0
	var limit = 100
	var instancesList []*instance.Instance

	for {
		response, err := c.client.Instance().ListInstance(ctx, &svcinstance.ListInstanceRequest{
			Limit:   int32(limit),
			Offset:  int32(offset),
			Filters: stringFiltersToFiltersMeta(c.Filters),
		})
		if err != nil {
			return err
		}
		instancesList = append(instancesList, response.GetInstances()...)
		if len(response.GetInstances()) == 0 {
			break
		}
		offset += limit
	}

	if c.Json {
		return PrintProtoListJson[*instance.Instance](instancesList)
	}

	if len(instancesList) == 0 {
		msg.Info("No instances found.")
		return nil
	}

	headers := []string{"Name", "Zone", "Ressource Type"}
	if c.ShowUuid {
		headers = append([]string{"UUID"}, headers...)
	}
	table := MakeTableWriter(headers)
	for _, instanceInfo := range instancesList {
		data := []string{
			msg.Yellow(instanceInfo.GetName()).String(),
			instanceInfo.GetZoneUuid(),
			instanceInfo.GetResourceType(),
		}
		if c.ShowUuid {
			data = append([]string{msg.Cyan(instanceInfo.GetUUID()).String()}, data...)
		}
		if err := table.Append(data); err != nil {
			return err
		}
	}
	return table.Render()
}
