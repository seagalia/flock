package reconciler

import (
	"context"
	"slices"

	"github.com/seagalia/flock/agent/f5/as3"
)

// ownTenant tells the agent may declare a tenant: it posted it already, or AS3 holds it with the remark of this
// instance, or no partition of that name exists. The agent never overwrites a partition someone else manages, by hand
// or through another AS3 user. A refusal is checked again on the next pass, the operator may have freed the name.
func (r *Reconciler) ownTenant(ctx context.Context, name string) (bool, error) {
	if r.owned[name] {
		return true, nil
	}
	tenants, err := r.dev.ListTenants(ctx)
	if err != nil {
		return false, err
	}
	if remark, isTenant := tenants[name]; isTenant {
		owned := remark == as3.TenantRemark(r.opts.InstanceUUID)
		r.owned[name] = owned
		return owned, nil
	}
	partitions, err := r.dev.ListPartitions(ctx)
	if err != nil {
		return false, err
	}
	if slices.Contains(partitions, name) {
		return false, nil
	}
	r.owned[name] = true
	return true, nil
}
