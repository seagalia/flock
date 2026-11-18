package cplters

import (
	"context"
	"fmt"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

type VIPComplete struct {
	VIP *vip.VIP
	VSS []*VirtualServerComplete
}

func (c *VIPComplete) ToMap() (map[string]any, error) {
	data, err := ProtoToMap(c.VIP)
	if err != nil {
		return nil, err
	}
	vssData := make([]map[string]any, 0)
	for _, vs := range c.VSS {
		vsData, err := vs.ToMap()
		if err != nil {
			return nil, err
		}
		vssData = append(vssData, vsData)
	}
	data["virtual_servers"] = vssData
	return data, nil
}

func (c *VIPComplete) Complete(ctx context.Context, client *clients.Client) error {
	vss := make([]*VirtualServerComplete, 0)

	for _, vsUuid := range c.VIP.GetVirtualServersUuid() {
		vsResp, err := client.VirtualServer().ReadVS(ctx, &svccore.UuidRequest{
			UUID: vsUuid,
		})
		if err != nil {
			return err
		}
		vsc := &VirtualServerComplete{
			VS: vsResp.GetVs(),
		}
		err = vsc.Complete(ctx, client)
		if err != nil {
			return fmt.Errorf("failed to complete virtual server %s: %w", vsUuid, err)
		}
		vss = append(vss, vsc)
	}
	c.VIP.VirtualServersUuid = nil
	c.VSS = vss
	return nil
}
