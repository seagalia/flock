package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/olekukonko/tablewriter"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type ListVip struct {
	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	ShowUuid   bool `short:"u" name:"show-uuid" help:"Show the uuid"`
	ShowParent bool `short:"p" name:"show-parent" help:"Show associated instances"`

	Instance InstanceNamed `short:"i" name:"instance" placeholder:"NAME|UUID" help:"List from instance with given name or uuid." predictor:"instance"`

	Filters []string `short:"f" name:"filter" sep:"none" help:"Filter by metadata, example foo==bar will find all which have key foo and value bar. Other comparison are == (equality), != (different), ~= (LIKE) and ~! (NOT LIKE). This can be set multiple times."`

	client *clients.Client
}

func (c *ListVip) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ListVip) Run(ctx context.Context) error {
	var offset, limit int32 = 0, 100
	var vipsList []*vip.VIP

	filters := stringFiltersToFiltersMeta(c.Filters)
	if c.Instance.Identifier() != "" {
		inst, err := FindInstance(ctx, c.client, &c.Instance)
		if err != nil {
			return err
		}
		filters = append(filters, &svccore.ListFilter{
			Key:      helpers.TypeUrl[*instance.Instance](),
			Value:    inst.GetUUID(),
			Operator: svccore.ListFilter_EQUAL,
		})
	}

	for {
		response, err := c.client.Vip().ListVIP(ctx, &svcvip.ListVIPRequest{
			Limit:   limit,
			Offset:  offset,
			Filters: filters,
		})
		if err != nil {
			return fmt.Errorf("unable to list vips: %s", err)
		}
		vipsList = append(vipsList, response.GetVip()...)
		if len(response.GetVip()) == 0 {
			break
		}
		offset += limit
	}

	if c.Json {
		return PrintProtoListJson[*vip.VIP](vipsList)
	}

	if len(vipsList) == 0 {
		msg.Info("No VIPs found.")
		return nil
	}

	var table *tablewriter.Table

	columns := []string{"Name", "Ip"}
	if c.ShowUuid {
		columns = append([]string{"UUID"}, columns...)
	}
	if c.ShowParent {
		columns = append(columns, "Parent Instances", "RD_UUID")
	}
	table = MakeTableWriter(columns)

	table.Options(tableWrap(false))

	for _, vipInfo := range vipsList {
		row := make([]string, 0)
		if c.ShowUuid {
			row = append(row, msg.Cyan(vipInfo.GetUUID()).String())
		}
		row = append(row, msg.Yellow(vipInfo.GetName()).String(), vipInfo.GetIp())
		if c.ShowParent {
			metadata := vipInfo.GetMetadata()
			rdUuid := "not set"
			if rdUuidFound, ok := metadata["rdUuid"]; ok {
				rdUuid = rdUuidFound
			}
			row = append(row, msg.Cyan(c.GetParentInstances(ctx, vipInfo.GetUUID())).String(), rdUuid)
		}
		if err := table.Append(row); err != nil {
			return err
		}
	}

	return table.Render()
}

func (c *ListVip) GetParentInstances(ctx context.Context, uuid string) string {
	inst, err := c.client.Instance().ListInstance(ctx,
		&svcinstance.ListInstanceRequest{
			Filters: []*svccore.ListFilter{
				{
					Key:   helpers.TypeUrl[*vip.VIP](),
					Value: uuid,
				},
			},
		})
	if err != nil || len(inst.GetInstances()) == 0 {
		return "Not used"
	}

	return inst.GetInstances()[0].GetName()
}
