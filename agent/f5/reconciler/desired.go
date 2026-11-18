package reconciler

import (
	"context"

	"fmt"
	"slices"
	"strings"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/agent/f5/config"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/proto"
)

type tenantRender struct {
	name   string
	vips   []*vip.VIP
	result as3.Result
	doc    []byte
}

func (t *tenantRender) vipUUIDs() []string {
	uuids := make([]string, 0, len(t.vips))
	for _, v := range t.vips {
		uuids = append(uuids, v.GetUUID())
	}
	return uuids
}

// desired renders every tenant from the stores. A VIP whose partition can't be a tenant is left out and reported in
// invalid, by VIP UUID.
func (r *Reconciler) desired(ctx context.Context) (tenants map[string]*tenantRender, invalid map[string]error, err error) {
	vips, err := r.store.VIPs().List(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("listing VIPs: %w", err)
	}
	in := as3.Input{
		InstanceUUID:   r.opts.InstanceUUID,
		VirtualServers: map[string]*vs.VirtualServer{},
		Backends:       map[string]*backend.Backend{},
		Certificates:   map[string]*cert.Certificate{},
		Endpoints:      map[string][]*core.IPAddr{},
	}
	if err := fill(ctx, in.VirtualServers, r.store.VirtualServers()); err != nil {
		return nil, nil, err
	}
	if err := fill(ctx, in.Backends, r.store.Backends()); err != nil {
		return nil, nil, err
	}
	if err := fill(ctx, in.Certificates, r.store.Certificates()); err != nil {
		return nil, nil, err
	}
	for uuid := range in.Backends {
		if endpoints, discovered := r.eps.Endpoints(uuid); discovered {
			in.Endpoints[uuid] = endpoints
		}
	}

	tenants = map[string]*tenantRender{}
	invalid = map[string]error{}
	for _, v := range vips {
		name := r.partitionOf(v)
		if err := config.ValidTenantName(name); err != nil {
			invalid[v.GetUUID()] = err
			continue
		}
		if tenants[name] == nil {
			tenants[name] = &tenantRender{name: name}
		}
		tenants[name].vips = append(tenants[name].vips, v)
	}
	for name, t := range tenants {
		tenantIn := in
		tenantIn.Tenant = name
		tenantIn.VIPs = t.vips
		slices.SortFunc(t.vips, func(a, b *vip.VIP) int { return strings.Compare(a.GetUUID(), b.GetUUID()) })
		t.result = as3.Render(tenantIn)
		t.doc, err = as3.Document(r.opts.InstanceUUID, name, t.result.Tenant, r.opts.DryRun)
		if err != nil {
			return nil, nil, fmt.Errorf("encoding tenant %s: %w", name, err)
		}
	}
	return tenants, invalid, nil
}

func (r *Reconciler) partitionOf(v *vip.VIP) string {
	if partition := v.GetMetadata()[r.opts.PartitionKey]; partition != "" {
		return partition
	}
	return r.opts.DefaultPartition
}

func fill[T proto.Message](ctx context.Context, into map[string]T, store storers.Storer[T]) error {
	items, err := store.List(ctx, nil)
	if err != nil {
		return fmt.Errorf("listing %s: %w", helpers.TypeUrl[T](), err)
	}
	for _, item := range items {
		into[helpers.GetIdentifier(item)] = item
	}
	return nil
}
