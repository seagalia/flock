package cli

import (
	"context"
	"fmt"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/sdk/clients"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
)

type RegisterVip struct {
	Name         string            `short:"n" name:"name" help:"Name of the vip you would like to register" required:""`
	Instance     InstanceNamed     `short:"i" name:"instance" xor:"location" help:"Instance (name or uuid) the vip is added to, its ip is taken in the zone of the instance" predictor:"instance"`
	Zone         ZoneNamed         `short:"z" name:"zone" xor:"location" help:"Zone (name or uuid) where the ip is taken" predictor:"zone"`
	ZoneMetadata map[string]string `short:"m" name:"zone-metadata" xor:"location" help:"Metadata (key=value) of the zone where the ip is taken"`
	Pool         string            `name:"pool" xor:"pool" help:"Pool of the zone where the ip is taken, its default pool otherwise"`
	PoolMetadata map[string]string `name:"pool-metadata" xor:"pool" help:"Metadata (key=value) of the pool where the ip is taken"`
	Json         bool              `short:"j" name:"json" help:"Print the vip details in json format"`

	client *clients.Client
}

func (c *RegisterVip) SetClient(client *clients.Client) {
	c.client = client
}

func (c *RegisterVip) Run(ctx context.Context) error {
	req, err := c.makeRequest(ctx)
	if err != nil {
		return err
	}

	response, err := c.client.Vip().RegisterVip(ctx, req)
	if err != nil {
		return err
	}

	if c.Json {
		return PrintProtoJson(response)
	}

	msg.Successf("Vip successfully registered with ip %s.", msg.Cyan(response.GetVip().GetIp()))
	return nil
}

// makeRequest relies on kong refusing two location flags or two pool flags together
func (c *RegisterVip) makeRequest(ctx context.Context) (*svcvip.RegisterVipRequest, error) {
	req := &svcvip.RegisterVipRequest{Name: c.Name}

	if c.Pool != "" {
		req.Pool = &svcvip.RegisterVipRequest_PoolName{PoolName: c.Pool}
	}
	if len(c.PoolMetadata) > 0 {
		req.Pool = &svcvip.RegisterVipRequest_PoolMetadata{PoolMetadata: &svcvip.MetadataSelector{Metadata: c.PoolMetadata}}
	}

	if c.Instance.Identifier() != "" {
		anInstance, err := FindInstance(ctx, c.client, &c.Instance)
		if err != nil {
			return nil, err
		}
		if err := refuseLocked("instance", anInstance.GetName(), anInstance.GetMetadata()); err != nil {
			return nil, err
		}
		req.Location = &svcvip.RegisterVipRequest_InstanceUuid{InstanceUuid: anInstance.GetUUID()}
		return req, nil
	}

	if c.Zone.Identifier() != "" {
		aZone, err := FindZone(ctx, c.client, &c.Zone)
		if err != nil {
			return nil, err
		}
		req.Location = &svcvip.RegisterVipRequest_ZoneUuid{ZoneUuid: aZone.GetUUID()}
		return req, nil
	}

	if len(c.ZoneMetadata) == 0 {
		return nil, fmt.Errorf("one of --instance, --zone or --zone-metadata is required")
	}
	req.Location = &svcvip.RegisterVipRequest_ZoneMetadata{ZoneMetadata: &svcvip.MetadataSelector{Metadata: c.ZoneMetadata}}
	return req, nil
}
