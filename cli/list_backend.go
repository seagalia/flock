package cli

import (
	"context"
	"fmt"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/olekukonko/tablewriter"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type ListBackend struct {
	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	VirtualServer VsNamed `short:"s" name:"vs" placeholder:"NAME|UUID" help:"List from virtual server with given name or uuid." predictor:"vs"`

	ShowUuid   bool `short:"u" name:"show-uuid" help:"Show the complete UUID"`
	ShowParent bool `short:"p" name:"show-parent" help:"Show associated virtual servers"`

	Filters []string `short:"f" name:"filter" sep:"none" help:"Filter by metadata, example foo==bar will find all which have key foo and value bar. Other comparison are == (equality), != (different), ~= (LIKE) and ~! (NOT LIKE). This can be set multiple times."`

	client *clients.Client
}

func (c *ListBackend) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ListBackend) Run(ctx context.Context) error {
	var offset = 0
	var limit = 100
	var backendsList []*backend.Backend

	filters := stringFiltersToFiltersMeta(c.Filters)
	if c.VirtualServer.Identifier() != "" {
		virtServ, err := FindVirtServ(ctx, c.client, &c.VirtualServer)
		if err != nil {
			return err
		}
		filters = append(filters, &svccore.ListFilter{
			Key:      helpers.TypeUrl[*vs.VirtualServer](),
			Value:    virtServ.GetUUID(),
			Operator: svccore.ListFilter_EQUAL,
		})
	}

	for {
		response, err := c.client.Backend().ListBackend(ctx, &svcbackend.ListBackendRequest{
			Limit:   int32(limit),
			Offset:  int32(offset),
			Filters: filters,
		})
		if err != nil {
			return err
		}
		backendsList = append(backendsList, response.GetBackends()...)
		if len(response.GetBackends()) == 0 {
			break
		}
		offset += limit
	}

	if c.Json {
		return PrintProtoListJson[*backend.Backend](backendsList)
	}

	if len(backendsList) == 0 {
		msg.Info("No backends found.")
		return nil
	}

	var table *tablewriter.Table
	columns := []string{"Name", "LB Policy", "HealthCheck Type", "Endpoint Config"}
	if c.ShowUuid {
		columns = append([]string{"UUID"}, columns...)
	}
	if c.ShowParent {
		columns = append(columns, "Parent Virtual Servers")
	}
	table = MakeTableWriter(columns)
	table.Options(tableWrap(true), tableBordered(), tableRowLines())

	for _, backendInfo := range backendsList {
		line := []string{
			msg.Yellow(backendInfo.GetName()).String(),
			backendInfo.GetLbPolicy().String(),
			getHealthCheckType(backendInfo),
			getEndpointConfig(backendInfo),
		}
		if c.ShowUuid {
			line = append([]string{
				msg.Cyan(backendInfo.GetUUID()).String(),
			}, line...)
		}
		if c.ShowParent {
			line = append(line, msg.Cyan(c.getParentVss(ctx, backendInfo.GetUUID())).String())
		}
		if err := table.Append(line); err != nil {
			return err
		}
	}
	return table.Render()
}

func getHealthCheckType(backendInfo *backend.Backend) string {
	healthCheckType := "HTTP"
	if backendInfo.GetHealthCheck().GetGrpcHealthCheck() != nil {
		healthCheckType = "GRPC"
	}
	if backendInfo.GetHealthCheck().GetTcpHealthCheck() != nil {
		healthCheckType = "TCP"
	}
	if backendInfo.GetHealthCheck().GetNoHealthCheck() != nil {
		healthCheckType = "NO"
	}
	return healthCheckType
}

func getEndpointConfig(backendInfo *backend.Backend) string {
	if backendInfo.GetIpAddrGroup() != nil {
		ips := make([]string, 0, len(backendInfo.GetIpAddrGroup().GetIpAddresses()))
		for _, ipAddr := range backendInfo.GetIpAddrGroup().GetIpAddresses() {
			ips = append(ips, ipAddr.Format())
		}
		return "Static: " + strings.Join(ips, " ")
	}
	if backendInfo.GetEds() != nil {
		return "EDS: " + backendInfo.GetEds().GetUrl().Format()
	}
	if backendInfo.GetDns() != nil {
		dnsInfo := fmt.Sprintf("DNS lookup %s for host %s",
			backendInfo.GetDns().GetLookupFamily().String(),
			backendInfo.GetDns().GetHost(),
		)
		if backendInfo.GetDns().GetPort() != 0 {
			dnsInfo += fmt.Sprintf(" port %d", backendInfo.GetDns().GetPort())
		}
		if backendInfo.GetDns().GetNameservers() != nil {
			ips := make([]string, 0)
			for _, ipAddr := range backendInfo.GetDns().GetNameservers() {
				ips = append(ips, ipAddr.Format())
			}
			dnsInfo += fmt.Sprintf("\nnameservers %s", strings.Join(ips, ",\n "))
		}
		return dnsInfo
	}
	return "Unknown"
}

func (c *ListBackend) getParentVss(ctx context.Context, backendUUID string) string {
	var offset, limit int32 = 0, 100
	vssName := make([]string, 0)
	for {
		response, err := c.client.VirtualServer().ListVS(ctx, &svcvs.ListVSRequest{
			Limit:  limit,
			Offset: offset,
			Filters: []*svccore.ListFilter{
				{
					Key:      helpers.TypeUrl[*backend.Backend](),
					Value:    backendUUID,
					Operator: svccore.ListFilter_EQUAL,
				},
			},
		})
		if err != nil {
			return fmt.Sprintf("Unable to get virtual servers: %s", err)
		}
		if len(response.GetVs()) == 0 {
			break
		}
		for _, vsInfo := range response.GetVs() {
			vssName = append(vssName, vsInfo.GetName())
		}
		offset += limit
	}

	return strings.Join(vssName, ",\n")
}

func CropUuid(uuid string) string {
	if len(uuid) > 8 {
		return uuid[:8]
	}
	return uuid
}
