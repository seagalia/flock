package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/olekukonko/tablewriter"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type ListVs struct {
	Json bool `short:"j" name:"json" help:"Print the virtual server in JSON format"`

	Vip VipNamed `short:"p" name:"vip" placeholder:"NAME|UUID" help:"List from vip with given name or uuid." predictor:"vip"`

	ShowUuid bool `short:"u" name:"show-uuid" help:"Show the complete UUID"`

	Filters []string `short:"f" name:"filter" sep:"none" help:"Filter by metadata, example foo==bar will find all which have key foo and value bar. Other comparison are == (equality), != (different), ~= (LIKE) and ~! (NOT LIKE). This can be set multiple times."`

	client *clients.Client
}

func (c *ListVs) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ListVs) Run(ctx context.Context) error {
	var offset = 0
	var limit = 100
	var vsList []*vs.VirtualServer

	filters := stringFiltersToFiltersMeta(c.Filters)
	if c.Vip.Identifier() != "" {
		v, err := FindVIP(ctx, c.client, &c.Vip)
		if err != nil {
			return err
		}
		filters = append(filters, &svccore.ListFilter{
			Key:      helpers.TypeUrl[*vip.VIP](),
			Value:    v.GetUUID(),
			Operator: svccore.ListFilter_EQUAL,
		})
	}

	for {
		response, err := c.client.VirtualServer().ListVS(ctx, &svcvs.ListVSRequest{
			Limit:   int32(limit),
			Offset:  int32(offset),
			Filters: filters,
		})
		if err != nil {
			return err
		}
		vsList = append(vsList, response.GetVs()...)
		if len(response.GetVs()) == 0 {
			break
		}
		offset += limit
	}

	if c.Json {
		return PrintProtoListJson[*vs.VirtualServer](vsList)
	}

	if len(vsList) == 0 {
		msg.Info("No virtual servers found.")
		return nil
	}

	var table *tablewriter.Table

	if c.ShowUuid {
		table = MakeTableWriter([]string{"UUID", "Name", "Port", "Route Type"})
	} else {
		table = MakeTableWriter([]string{"Name", "Port", "Route Type"})
	}

	table.Options(tableBordered(), tableRowLines())

	for _, vsInfo := range vsList {
		var row []string
		if c.ShowUuid {
			row = append(row, msg.Cyan(vsInfo.GetUUID()).String())
		}
		row = append(row, msg.Yellow(vsInfo.GetName()).String(), fmt.Sprintf("%d", vsInfo.GetPort()), msg.Cyan(getRouteType(vsInfo)).String())
		if err := table.Append(row); err != nil {
			return err
		}
	}
	return table.Render()
}

func getRouteType(vsInfo *vs.VirtualServer) string {
	if vsInfo.GetTcpRouteConfig() != nil {
		return "TCP"
	}
	if vsInfo.GetUdpRouteConfig() != nil {
		return "UDP"
	}
	if vsInfo.GetHttpRoutesConfig() != nil {
		return "HTTP"
	}
	if vsInfo.GetTlsTcpRoutesConfig() != nil {
		return "TLS over TCP"
	}
	return "UNKNOWN"
}
