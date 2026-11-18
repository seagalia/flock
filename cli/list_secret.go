package cli

import (
	"context"
	"slices"
	"strconv"
	"strings"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type ListSecret struct {
	Json     bool `short:"j" name:"json" help:"Format in json instead of human table readable."`
	ShowUuid bool `short:"u" name:"show-uuid" help:"Show the complete UUID"`

	Filters []string `short:"f" name:"filter" sep:"none" help:"Filter by metadata, example foo==bar will find all which have key foo and value bar. Other comparison are == (equality), != (different), ~= (LIKE) and ~! (NOT LIKE). This can be set multiple times."`

	client *clients.Client
}

func (c *ListSecret) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ListSecret) Run(ctx context.Context) error {
	var offset = 0
	var limit = 100
	var secretsList []*secret.Secret

	filters := stringFiltersToFiltersMeta(c.Filters)
	for {
		response, err := c.client.Secret().ListSecret(ctx, &svcsecret.ListSecretRequest{
			Limit:   int32(limit),
			Offset:  int32(offset),
			Filters: filters,
		})
		if err != nil {
			return err
		}
		if len(response.GetSecrets()) == 0 {
			break
		}

		secretsList = append(secretsList, response.GetSecrets()...)
		offset += limit
	}

	if c.Json {
		blanked := make([]*secret.Secret, 0, len(secretsList))
		for _, listed := range secretsList {
			blanked = append(blanked, withoutSecretValues(listed))
		}
		return PrintProtoListJson[*secret.Secret](blanked)
	}

	if len(secretsList) == 0 {
		msg.Info("No secrets found.")
		return nil
	}
	headers := []string{"Name", "Keys", "Used by"}
	if c.ShowUuid {
		headers = append([]string{"UUID"}, headers...)
	}
	table := MakeTableWriter(headers)
	for _, listed := range secretsList {
		usedBy, err := countSecretUsers(ctx, c.client, listed.GetUUID())
		if err != nil {
			return err
		}
		keys := make([]string, 0, len(listed.GetData()))
		for key := range listed.GetData() {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		data := []string{
			msg.Yellow(listed.GetName()).String(),
			strings.Join(keys, ", "),
			strconv.Itoa(usedBy),
		}
		if c.ShowUuid {
			data = append([]string{msg.Cyan(listed.GetUUID()).String()}, data...)
		}
		if err := table.Append(data); err != nil {
			return err
		}
	}
	return table.Render()
}

func countSecretUsers(ctx context.Context, client *clients.Client, secretUuid string) (int, error) {
	filters := []*svccore.ListFilter{{
		Key:      helpers.TypeUrl[*secret.Secret](),
		Value:    secretUuid,
		Operator: svccore.ListFilter_EQUAL,
	}}
	const limit = 100

	listers := []func(offset int32) (int, error){
		func(offset int32) (int, error) {
			resp, err := client.VirtualServer().ListVS(ctx, &svcvs.ListVSRequest{Limit: limit, Offset: offset, Filters: filters})
			return len(resp.GetVs()), err
		},
		func(offset int32) (int, error) {
			resp, err := client.Backend().ListBackend(ctx, &svcbackend.ListBackendRequest{Limit: limit, Offset: offset, Filters: filters})
			return len(resp.GetBackends()), err
		},
		func(offset int32) (int, error) {
			resp, err := client.Vip().ListVIP(ctx, &svcvip.ListVIPRequest{Limit: limit, Offset: offset, Filters: filters})
			return len(resp.GetVip()), err
		},
		func(offset int32) (int, error) {
			resp, err := client.Certificate().ListCertificate(ctx, &svccert.ListCertificateRequest{Limit: limit, Offset: offset, Filters: filters})
			return len(resp.GetCertificates()), err
		},
		func(offset int32) (int, error) {
			resp, err := client.Zone().ListZone(ctx, &svczone.ListZoneRequest{Limit: limit, Offset: offset, Filters: filters})
			return len(resp.GetZones()), err
		},
		func(offset int32) (int, error) {
			resp, err := client.Instance().ListInstance(ctx, &svcinstance.ListInstanceRequest{Limit: limit, Offset: offset, Filters: filters})
			return len(resp.GetInstances()), err
		},
	}

	total := 0
	for _, list := range listers {
		for offset := int32(0); ; offset += limit {
			found, err := list(offset)
			if err != nil {
				return 0, err
			}
			if found == 0 {
				break
			}
			total += found
		}
	}
	return total, nil
}
