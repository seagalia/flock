package discoverers

import (
	"context"
	"fmt"

	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
)

//go:generate go tool mockgen -destination=../testhelpers/mock_discoverer.go -package=testhelpers . Discoverer

type Discoverer interface {
	// Run hands a channel to the discovery provider (Consul, DNS, etc.) through which
	// it can send updated target groups. It must return when the context is canceled.
	// It should not close the update channel on returning.
	Run(ctx context.Context, found chan<- []*core.IPAddr) error
}

// MakeDiscoverer gives the discoverer of the endpoints of backendDef, which reports its refresh failures in states.
func MakeDiscoverer(backendDef *backend.Backend, states *stores.StateStore) (Discoverer, error) {
	var disc Discoverer
	var err error
	switch backendDef.GetEndpoint().(type) {
	case *backend.Backend_IpAddrGroup:
		// a static backend is a backend with a fixed list of endpoints, so we don't need to refresh it
		return NewStatic(backendDef.GetIpAddrGroup().GetIpAddresses()), nil
	case *backend.Backend_Dns:
		disc, err = NewDns(
			backendDef.GetDns().GetHost(),
			backendDef.GetDns().GetPort(),
			backendDef.GetDns().GetLookupFamily(),
			backendDef.GetDns().GetNameservers()...,
		)
	case *backend.Backend_Eds:
		disc, err = NewEds(
			backendDef.GetEds().GetUrl(),
			backendDef.GetEds().GetServiceName(),
			backendDef.GetEds().GetCa(),
			core.HeaderValuesToMap(backendDef.GetEds().GetHeaders()),
		)
	default:
		return nil, fmt.Errorf("unsupported backend endpoint %T", backendDef.GetEndpoint())
	}
	if err != nil {
		return nil, err
	}
	disc = NewRefresh(backendDef, disc, states)
	return disc, nil
}
