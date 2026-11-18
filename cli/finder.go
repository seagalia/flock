package cli

import (
	"context"
	"fmt"

	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

func FindFirstByName[T proto.Message](ctx context.Context, client *clients.Client, name string, operators ...svccore.ListFilter_Operator) (T, error) {
	var mess T
	elem, err := FindFirstTypedByName(ctx, client, helpers.TypeUrl[T](), name, operators...)
	if err != nil {
		return mess, err
	}
	return elem.(T), nil
}

func TypedProtosToGeneric[T proto.Message](elems []T) []proto.Message {
	mess := make([]proto.Message, 0, len(elems))
	for _, elem := range elems {
		mess = append(mess, elem)
	}
	return mess
}

func FindTypedByName(ctx context.Context, client *clients.Client, typeUrl string, name string, operators ...svccore.ListFilter_Operator) ([]proto.Message, error) {
	operator := svccore.ListFilter_EQUAL
	if len(operators) > 0 {
		operator = operators[0]
	}
	filterName := make([]*svccore.ListFilter, 0)
	if name != "" {
		filterName = []*svccore.ListFilter{
			{
				Key:      "name",
				Value:    name,
				Operator: operator,
			},
		}
	}

	switch typeUrl {
	case helpers.TypeUrl[*instance.Instance]():
		resp, err := client.Instance().ListInstance(ctx, &svcinstance.ListInstanceRequest{
			Filters: filterName,
		})
		if err != nil {
			return nil, err
		}
		return TypedProtosToGeneric[*instance.Instance](resp.GetInstances()), nil
	case helpers.TypeUrl[*backend.Backend]():
		resp, err := client.Backend().ListBackend(ctx, &svcbackend.ListBackendRequest{
			Filters: filterName,
		})
		if err != nil {
			return nil, err
		}

		return TypedProtosToGeneric[*backend.Backend](resp.GetBackends()), nil
	case helpers.TypeUrl[*vip.VIP]():
		resp, err := client.Vip().ListVIP(ctx, &svcvip.ListVIPRequest{
			Filters: filterName,
		})
		if err != nil {
			return nil, err
		}
		return TypedProtosToGeneric[*vip.VIP](resp.GetVip()), nil
	case helpers.TypeUrl[*vs.VirtualServer]():
		resp, err := client.VirtualServer().ListVS(ctx, &svcvs.ListVSRequest{
			Filters: filterName,
		})
		if err != nil {
			return nil, err
		}
		return TypedProtosToGeneric[*vs.VirtualServer](resp.GetVs()), nil
	case helpers.TypeUrl[*cert.Certificate]():
		resp, err := client.Certificate().ListCertificate(ctx, &svccert.ListCertificateRequest{
			Filters: filterName,
		})
		if err != nil {
			return nil, err
		}
		return TypedProtosToGeneric[*cert.Certificate](resp.GetCertificates()), nil
	case helpers.TypeUrl[*secret.Secret]():
		resp, err := client.Secret().ListSecret(ctx, &svcsecret.ListSecretRequest{
			Filters: filterName,
		})
		if err != nil {
			return nil, err
		}
		return TypedProtosToGeneric[*secret.Secret](resp.GetSecrets()), nil
	case helpers.TypeUrl[*zone.Zone]():
		resp, err := client.Zone().ListZone(ctx, &svczone.ListZoneRequest{
			Filters: filterName,
		})
		if err != nil {
			return nil, err
		}
		return TypedProtosToGeneric[*zone.Zone](resp.GetZones()), nil
	default:
		return nil, fmt.Errorf("type %s not found", typeUrl)
	}
}

func FindFirstTypedByName(ctx context.Context, client *clients.Client, typeUrl string, name string, operators ...svccore.ListFilter_Operator) (proto.Message, error) {
	elems, err := FindTypedByName(ctx, client, typeUrl, name, operators...)
	if err != nil {
		return nil, err
	}
	if len(elems) == 0 {
		return nil, fmt.Errorf("%s not found", name)
	}
	return elems[0], nil
}

func FindBackend(ctx context.Context, client *clients.Client, named Named) (*backend.Backend, error) {
	if !named.IsUUID() {
		return FindFirstByName[*backend.Backend](ctx, client, named.Identifier())
	}

	response, err := client.Backend().ReadBackend(ctx, &svccore.UuidRequest{UUID: named.Identifier()})
	if err != nil {
		return nil, err
	}
	return response.GetBackend(), nil
}

func FindCertificate(ctx context.Context, client *clients.Client, named Named) (*cert.Certificate, error) {
	if !named.IsUUID() {
		return FindFirstByName[*cert.Certificate](ctx, client, named.Identifier())
	}

	response, err := client.Certificate().ReadCertificate(ctx, &svccore.UuidRequest{UUID: named.Identifier()})
	if err != nil {
		return nil, err
	}
	return response.GetCertificate(), nil
}

func FindSecret(ctx context.Context, client *clients.Client, named Named) (*secret.Secret, error) {
	if !named.IsUUID() {
		return FindFirstByName[*secret.Secret](ctx, client, named.Identifier())
	}

	response, err := client.Secret().ReadSecret(ctx, &svccore.UuidRequest{UUID: named.Identifier()})
	if err != nil {
		return nil, err
	}
	return response.GetSecret(), nil
}

func FindInstance(ctx context.Context, client *clients.Client, named Named) (*instance.Instance, error) {
	if !named.IsUUID() {
		return FindFirstByName[*instance.Instance](ctx, client, named.Identifier())
	}

	response, err := client.Instance().ReadInstance(ctx, &svccore.UuidRequest{UUID: named.Identifier()})
	if err != nil {
		return nil, err
	}
	return response.GetInstance(), nil
}

func FindVIP(ctx context.Context, client *clients.Client, named Named) (*vip.VIP, error) {
	if !named.IsUUID() {
		return FindFirstByName[*vip.VIP](ctx, client, named.Identifier())
	}

	response, err := client.Vip().ReadVIP(ctx, &svccore.UuidRequest{UUID: named.Identifier()})
	if err != nil {
		return nil, err
	}
	return response.GetVip(), nil
}

func FindVirtServ(ctx context.Context, client *clients.Client, named Named) (*vs.VirtualServer, error) {
	if !named.IsUUID() {
		return FindFirstByName[*vs.VirtualServer](ctx, client, named.Identifier())
	}

	response, err := client.VirtualServer().ReadVS(ctx, &svccore.UuidRequest{UUID: named.Identifier()})
	if err != nil {
		return nil, err
	}
	return response.GetVs(), nil
}

// FindPoolOfZone returns the pool of the zone with that name.
func FindPoolOfZone(aZone *zone.Zone, poolName string) (*zone.IpPool, error) {
	for _, pool := range aZone.GetPools() {
		if pool.GetName() == poolName {
			return pool, nil
		}
	}
	return nil, fmt.Errorf("zone %s has no pool %s", aZone.GetName(), poolName)
}

func FindZone(ctx context.Context, client *clients.Client, named Named) (*zone.Zone, error) {
	if !named.IsUUID() {
		return FindFirstByName[*zone.Zone](ctx, client, named.Identifier())
	}

	response, err := client.Zone().ReadZone(ctx, &svccore.UuidRequest{UUID: named.Identifier()})
	if err != nil {
		return nil, err
	}
	return response.GetZone(), nil
}
