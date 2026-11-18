package devenv

import (
	"context"
	"fmt"
	"slices"

	"github.com/seagalia/flock/cli/app"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/types/known/anypb"
)

// loginAdmin logs the admin in like `flock login` does and writes the cli config in cliConfigPath,
// the cli commands use it while the session runs.
func loginAdmin(ctx context.Context, cliConfigPath, controllerAddr, password string) (*clients.Client, error) {
	cnf := &app.Config{
		Host:       controllerAddr,
		SkipVerify: true,
		Username:   adminUser,
		Password:   password,
	}
	// the token of a previous session was signed by another key
	app.ClearLogin(cliConfigPath, cnf)
	// CreateClient logs in eagerly, bounded by ctx and the login call timeout
	conn, _, err := app.CreateClient(ctx, cliConfigPath, cnf, false)
	if err != nil {
		return nil, err
	}
	return clients.New(conn), nil
}

// seed makes sure, through the api as a user would, the zone, the vip and the instance the agent serves
// exist. The store is kept across sessions: what a previous session created, and maybe the user changed
// since, is used as is, except the ip of the vip, set back to the wildcard. It gives the instance uuid.
func seed(ctx context.Context, client *clients.Client) (string, error) {
	zoneUuid, err := seedZone(ctx, client)
	if err != nil {
		return "", err
	}
	vipUuid, err := seedVip(ctx, client)
	if err != nil {
		return "", err
	}
	return seedInstance(ctx, client, vipUuid, zoneUuid)
}

func nameFilter(name string) []*svccore.ListFilter {
	return []*svccore.ListFilter{{Key: storers.FilterNameKey, Operator: svccore.ListFilter_EQUAL, Value: name}}
}

func seedZone(ctx context.Context, client *clients.Client) (string, error) {
	existing, err := client.Zone().ListZone(ctx, &svczone.ListZoneRequest{Filters: nameFilter(zoneName)})
	if err != nil {
		return "", fmt.Errorf("list zones: %w", err)
	}
	if len(existing.GetZones()) > 0 {
		return existing.GetZones()[0].GetUUID(), nil
	}
	created, err := client.Zone().CreateZone(ctx, &svczone.CreateZoneRequest{Zone: &zone.Zone{
		// no pools: the ipam is off in dev, the vip is the wildcard
		Name: zoneName,
	}})
	if err != nil {
		return "", fmt.Errorf("create zone: %w", err)
	}
	return created.GetZone().GetUUID(), nil
}

func seedVip(ctx context.Context, client *clients.Client) (string, error) {
	existing, err := client.Vip().ListVIP(ctx, &svcvip.ListVIPRequest{Filters: nameFilter(vipName)})
	if err != nil {
		return "", fmt.Errorf("list vips: %w", err)
	}
	if len(existing.GetVip()) > 0 {
		stored := existing.GetVip()[0]
		if stored.GetIp() == vipIp {
			return stored.GetUUID(), nil
		}
		// the update replaces the stored vip, so it is sent whole to keep its virtual servers
		stored.Ip = vipIp
		updated, err := client.Vip().UpdateVIP(ctx, &svcvip.UpdateVIPRequest{UUID: stored.GetUUID(), Vip: stored})
		if err != nil {
			return "", fmt.Errorf("update vip: %w", err)
		}
		return updated.GetVip().GetUUID(), nil
	}
	// starling never announces the wildcard whatever it says, STATIC tells the reader so
	noAnnounce, err := anypb.New(&starling_announce.StarlingAnnounce{TypeAnnounce: starling_announce.StarlingAnnounce_STATIC})
	if err != nil {
		return "", err
	}
	created, err := client.Vip().CreateVIP(ctx, &svcvip.CreateVIPRequest{Vip: &vip.VIP{
		Name:          vipName,
		Ip:            vipIp,
		TypedAnnounce: noAnnounce,
	}})
	if err != nil {
		return "", fmt.Errorf("create vip: %w", err)
	}
	return created.GetVip().GetUUID(), nil
}

func seedInstance(ctx context.Context, client *clients.Client, vipUuid string, zoneUuid string) (string, error) {
	existing, err := client.Instance().ListInstance(ctx, &svcinstance.ListInstanceRequest{Filters: nameFilter(instanceName)})
	if err != nil {
		return "", fmt.Errorf("list instances: %w", err)
	}
	if len(existing.GetInstances()) > 0 {
		stored := existing.GetInstances()[0]
		if slices.Contains(stored.GetVipsUuid(), vipUuid) {
			return stored.GetUUID(), nil
		}
		_, err := client.Instance().AddVipToInstance(ctx, &svcinstance.AddVipToInstanceRequest{UUID: stored.GetUUID(), VipUuid: vipUuid})
		if err != nil {
			return "", fmt.Errorf("add vip to instance: %w", err)
		}
		return stored.GetUUID(), nil
	}
	created, err := client.Instance().CreateInstance(ctx, &svcinstance.CreateInstanceRequest{Instance: &instance.Instance{
		Name:         instanceName,
		ZoneUuid:     zoneUuid,
		ResourceType: resourceTypeName,
		VipsUuid:     []string{vipUuid},
	}})
	if err != nil {
		return "", fmt.Errorf("create instance: %w", err)
	}
	return created.GetInstance().GetUUID(), nil
}
