package cli

import (
	"context"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type ListCertificate struct {
	Json bool `short:"j" name:"json" help:"Format in json instead of human table readable."`

	VirtualServer VsNamed `short:"s" name:"vs" placeholder:"NAME|UUID" help:"List from virtual server with given name or uuid." predictor:"vs"`
	ShowUuid      bool    `short:"u" name:"show-uuid" help:"Show the complete UUID"`

	Filters []string `short:"f" name:"filter" sep:"none" help:"Filter by metadata, example foo==bar will find all which have key foo and value bar. Other comparison are == (equality), != (different), ~= (LIKE) and ~! (NOT LIKE). This can be set multiple times."`

	client *clients.Client
}

func (c *ListCertificate) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ListCertificate) Run(ctx context.Context) error {
	var offset = 0
	var limit = 100
	var certsList []*cert.Certificate

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
		response, err := c.client.Certificate().ListCertificate(ctx, &svccert.ListCertificateRequest{
			Limit:   int32(limit),
			Offset:  int32(offset),
			Filters: filters,
		})
		if err != nil {
			return err
		}
		if len(response.GetCertificates()) == 0 {
			break
		}

		certsList = append(certsList, response.GetCertificates()...)
		offset += limit
	}

	if c.Json {
		return PrintProtoListJson[*cert.Certificate](certsList)
	}

	if len(certsList) == 0 {
		msg.Info("No certificates found.")
		return nil
	}
	headers := []string{"Name", "Domains"}
	if c.ShowUuid {
		headers = append([]string{"UUID"}, headers...)
	}
	table := MakeTableWriter(headers)
	for _, certInfo := range certsList {
		data := []string{
			msg.Yellow(certInfo.GetName()).String(),
			strings.Join(certInfo.GetDomainNames(), ", "),
		}
		if c.ShowUuid {
			data = append([]string{msg.Cyan(certInfo.GetUUID()).String()}, data...)
		}
		if err := table.Append(data); err != nil {
			return err
		}
	}
	return table.Render()
}
