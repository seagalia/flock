package cplters

import (
	"context"
	"fmt"

	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

type InstanceComplete struct {
	Instance *instance.Instance
	VIPs     []*VIPComplete
	Zone     *zone.Zone
}

func (c *InstanceComplete) ToMap() (map[string]any, error) {
	data, err := ProtoToMap(c.Instance)
	if err != nil {
		return nil, err
	}
	vipsData := make([]map[string]any, 0)
	for _, vs := range c.VIPs {
		vipData, err := vs.ToMap()
		if err != nil {
			return nil, err
		}
		vipsData = append(vipsData, vipData)
	}
	data["vips"] = vipsData
	return data, nil
}

func (c *InstanceComplete) Complete(ctx context.Context, client *clients.Client) error {
	vips := make([]*VIPComplete, 0)

	zoneExp := c.Instance.GetZoneUuid()
	zoneResp, err := client.Zone().ReadZone(ctx, &svccore.UuidRequest{
		UUID: zoneExp,
	})
	if err != nil {
		return err
	}

	for _, vsUuid := range c.Instance.GetVipsUuid() {
		vipResp, err := client.Vip().ReadVIP(ctx, &svccore.UuidRequest{
			UUID: vsUuid,
		})
		if err != nil {
			return err
		}
		vipc := &VIPComplete{
			VIP: vipResp.GetVip(),
		}
		err = vipc.Complete(ctx, client)
		if err != nil {
			return fmt.Errorf("failed to complete vip %s: %w", vsUuid, err)
		}
		vips = append(vips, vipc)
	}
	c.Instance.VipsUuid = nil
	c.VIPs = vips
	c.Zone = zoneResp.GetZone()
	return nil
}
