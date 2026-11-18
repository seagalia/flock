package cli

import (
	"context"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
)

type ListZone struct {
	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	Filters []string `short:"f" name:"filter" sep:"none" help:"Filter by metadata, example foo==bar will find all which have key foo and value bar. Other comparison are == (equality), != (different), ~= (LIKE) and ~! (NOT LIKE). This can be set multiple times."`

	client *clients.Client
}

func (c *ListZone) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ListZone) Run(ctx context.Context) error {
	var offset = 0
	var limit = 100
	var zonesList []*zone.Zone

	filters := stringFiltersToFiltersMeta(c.Filters)
	for {
		response, err := c.client.Zone().ListZone(ctx, &svczone.ListZoneRequest{
			Limit:   int32(limit),
			Offset:  int32(offset),
			Filters: filters,
		})
		if err != nil {
			return err
		}
		zonesList = append(zonesList, response.GetZones()...)
		if len(response.GetZones()) == 0 {
			break
		}
		offset += limit
	}

	if c.Json {
		return PrintProtoListJson[*zone.Zone](zonesList)
	}

	if len(zonesList) == 0 {
		msg.Info("No Zones found.")
		return nil
	}

	table := MakeTableWriter([]string{"Name", "Zone", "Pools"})
	for _, zoneInfo := range zonesList {
		if err := table.Append([]string{
			msg.Cyan(zoneInfo.GetName()).String(),
			msg.Yellow(zoneInfo.GetZone()).String(),
			poolNames(zoneInfo),
		}); err != nil {
			return err
		}
	}
	return table.Render()
}

func poolNames(zoneInfo *zone.Zone) string {
	defaultPool := zoneInfo.DefaultPool()
	names := make([]string, 0, len(zoneInfo.GetPools()))
	for _, pool := range zoneInfo.GetPools() {
		name := pool.GetName()
		if pool == defaultPool {
			name += " (default)"
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}
