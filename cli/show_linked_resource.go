package cli

import (
	"context"
	"encoding/json"
	"os"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type LinkedResource struct {
	Name string `json:"name"`
	Uuid string `json:"uuid"`
	Type string `json:"type"`
}

type ShowLinkedResource struct {
	ResourceType string `short:"r" name:"resource-type" help:"Resource type" required:"" enum:"instance,backend,vip,vs,certificate"`

	Named NameUuid `arg:"" name:"resource" help:"resource name or uuid" predictor:"by-resource-type"`

	Json bool `short:"j" name:"json" help:"Print the linked resource in JSON format"`

	client *clients.Client
}

func (c *ShowLinkedResource) SetClient(client *clients.Client) {
	c.client = client
}

func (c *ShowLinkedResource) Run(ctx context.Context) error {
	resType := TypeFromShort(c.ResourceType)

	uuid := c.Named.Identifier()
	if !c.Named.IsUUID() {
		res, err := FindFirstTypedByName(ctx, c.client, resType, c.Named.Identifier())
		if err != nil {
			return err
		}
		uuid = helpers.GetIdentifier(res)
	}

	var response []LinkedResource

	switch c.ResourceType {
	case "instance":
		msg.Errorf("To find instance %s, simply read a %s instance and you'll have all %s associated", msg.Yellow("linked resources"), msg.Underline("single"), msg.Yellow("vips"))
		return nil
	case "vip":
		response = c.GetParentInstances(ctx, uuid)
	case "vs":
		response = c.GetParentVips(ctx, uuid)
	case "backend", "certificate":
		response = c.GetParentVss(ctx, uuid)
	}

	if len(response) == 0 {
		msg.Errorf("No linked resource found")
		return nil
	}

	uniqueLinkedResources := make(map[string]LinkedResource)
	for _, linkedResource := range response {
		uniqueLinkedResources[linkedResource.Uuid] = linkedResource
	}

	uniqueResponse := make([]LinkedResource, 0, len(uniqueLinkedResources))
	for _, linkedResource := range uniqueLinkedResources {
		uniqueResponse = append(uniqueResponse, linkedResource)
	}

	if c.Json {
		data, err := json.Marshal(uniqueResponse)
		if err != nil {
			return err
		}
		os.Stdout.Write(data)
		return nil
	}

	table := MakeTableWriter([]string{"Linked Resource Type", "Linked Resource UUID", "Linked Resource Name"}, os.Stdout)
	table.Options(tableBordered())
	for _, linkedResource := range uniqueResponse {
		if err := table.Append([]string{
			msg.Green(linkedResource.Type).String(),
			msg.Cyan(linkedResource.Uuid).String(),
			msg.Yellow(linkedResource.Name).String(),
		}); err != nil {
			return err
		}
	}
	return table.Render()
}

func (c *ShowLinkedResource) GetParentInstances(ctx context.Context, uuid string) []LinkedResource {
	var offset = 0
	var limit = 100
	var instanceList []*instance.Instance

	for {
		response, err := c.client.Instance().ListInstance(
			ctx,
			&svcinstance.ListInstanceRequest{
				Filters: []*svccore.ListFilter{
					{
						Key:   helpers.TypeUrl[*vip.VIP](),
						Value: uuid,
					},
				},
				Limit:  int32(limit),
				Offset: int32(offset),
			},
		)
		if err != nil {
			panic(err)
		}
		instanceList = append(instanceList, response.GetInstances()...)
		if len(response.GetInstances()) == 0 {
			break
		}
		offset += limit

	}

	if len(instanceList) == 0 {
		return nil
	}

	var linkedResources []LinkedResource
	for _, inst := range instanceList {
		linkedResources = append(linkedResources, LinkedResource{
			Name: inst.GetName(),
			Uuid: inst.GetUUID(),
			Type: "instance",
		})
	}

	return linkedResources
}

func (c *ShowLinkedResource) GetParentVips(ctx context.Context, uuid string) []LinkedResource {
	var offset = 0
	var limit = 100
	var vipsList []*vip.VIP

	for {
		response, err := c.client.Vip().ListVIP(
			ctx,
			&svcvip.ListVIPRequest{
				Filters: []*svccore.ListFilter{
					{
						Key:   helpers.TypeUrl[*vs.VirtualServer](),
						Value: uuid,
					},
				},
				Limit:  int32(limit),
				Offset: int32(offset),
			},
		)
		if err != nil {
			panic(err)
		}
		vipsList = append(vipsList, response.GetVip()...)
		if len(response.GetVip()) == 0 {
			break
		}
		offset += limit
	}

	if len(vipsList) == 0 {
		return nil
	}

	var linkedResources []LinkedResource
	for _, aVip := range vipsList {
		linkedResources = append(linkedResources, LinkedResource{
			Name: aVip.GetName(),
			Uuid: aVip.GetUUID(),
			Type: "vip",
		})
		linkedResources = append(linkedResources, c.GetParentInstances(ctx, aVip.GetUUID())...)
	}

	return linkedResources
}

func (c *ShowLinkedResource) GetParentVss(ctx context.Context, uuid string) []LinkedResource {
	var offset = 0
	var limit = 100
	var vssList []*vs.VirtualServer

	for {
		response, err := c.client.VirtualServer().ListVS(
			ctx,
			&svcvs.ListVSRequest{
				Filters: []*svccore.ListFilter{
					{
						Key:   helpers.TypeUrl[*backend.Backend](),
						Value: uuid,
					},
					{
						Key:   helpers.TypeUrl[*cert.Certificate](),
						Value: uuid,
					},
				},
				Limit:  int32(limit),
				Offset: int32(offset),
			},
		)
		if err != nil {
			panic(err)
		}
		vssList = append(vssList, response.GetVs()...)
		if len(response.GetVs()) == 0 {
			break
		}
		offset += limit
	}

	if len(vssList) == 0 {
		return nil
	}

	var linkedResources []LinkedResource
	for _, virtServ := range vssList {
		linkedResources = append(linkedResources, LinkedResource{
			Name: virtServ.GetName(),
			Uuid: virtServ.GetUUID(),
			Type: "vs",
		})
		linkedResources = append(linkedResources, c.GetParentVips(ctx, virtServ.GetUUID())...)
	}

	return linkedResources
}
