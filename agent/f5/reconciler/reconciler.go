// Package reconciler keeps the AS3 tenants of a BIG-IP equal to the flock resources the agent received. Handlers only
// call Changed: one goroutine renders every tenant from the stores and posts the ones whose declaration changed, so
// the device sees one declaration per tenant at a time and the same stores always give the same declarations.
package reconciler

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"github.com/seagalia/flock/sdk/storers"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/agent/f5/bigip"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/logging"
)

type Device interface {
	Declare(ctx context.Context, tenant string, body []byte) (bigip.DeclareResult, error)
	DeleteTenant(ctx context.Context, tenant string) error
	ListTenants(ctx context.Context) (map[string]string, error)
	ListPartitions(ctx context.Context) ([]string, error)
}

// EndpointSource gives the discovered endpoints of a backend, false until its first discovery.
type EndpointSource interface {
	Endpoints(backendUUID string) ([]*core.IPAddr, bool)
}

type Options struct {
	InstanceUUID     string
	DefaultPartition string
	// PartitionKey is the VIP metadata key naming its partition.
	PartitionKey string
	// Debounce is the quiet time after a change before posting, MaxDebounce bounds the wait under constant changes.
	Debounce    time.Duration
	MaxDebounce time.Duration
	// MaxIncomplete is how long a tenant waits for the resources it references before it is posted without them.
	MaxIncomplete time.Duration
	// ResyncInterval reposts every tenant, which corrects changes made on the device by hand.
	ResyncInterval time.Duration
	// GarbageAfterStart is the wait before deleting the tenants of this instance no VIP uses: the controller must have
	// sent everything first.
	GarbageAfterStart time.Duration
	DryRun            bool
	// RetryMin and RetryMax bound the backoff after a retryable failure, 1s and 1m when zero.
	RetryMin time.Duration
	RetryMax time.Duration
}

type Status struct {
	FailedTenants map[string]string
	PostedVIPs    []*vip.VIP
}

// postedTenant is what the device holds of a tenant, as the last post left it.
type postedTenant struct {
	doc []byte
	// vips and pools are what the device holds: after a refusal AS3 rolled back to the previous ones
	vips []*vip.VIP
	// failed tells the device refused doc: it is posted again when it changes or on resync
	failed bool
	pools  []as3.PoolRef
}

type Reconciler struct {
	opts Options
	dev  Device
	eps  EndpointSource
	// store holds the resources the controller sent, secret references resolved
	store       storers.StoreManager
	stateStore  *stores.StateStore
	secretLinks *stores.SecretLinks
	changed     chan struct{}
	synced      chan struct{}
	syncedOnce  sync.Once

	mu     sync.Mutex
	status Status
	pools  []as3.PoolRef

	// the fields below belong to the Run goroutine
	posted          map[string]postedTenant
	owned           map[string]bool
	incompleteSince map[string]time.Time
	// failures holds why the device refused a tenant, or why the agent can't declare it
	failures map[string]error
	// vipTenant is the tenant each VIP was last posted in, to tell when one moved
	vipTenant map[string]string
	// notes are the texts of the last post of a tenant on its resources (move, drift, missing references)
	notes map[noteKey][]string
	// warned and errored are the resources the agent set notes or an error on, to clear them once gone
	warned     map[as3.ResourceKey]bool
	errored    map[as3.ResourceKey]bool
	adding     map[as3.ResourceKey]bool
	retryDelay time.Duration
}

func New(
	o Options, dev Device, eps EndpointSource,
	store storers.StoreManager, stateStore *stores.StateStore, secretLinks *stores.SecretLinks,
) *Reconciler {
	o.RetryMin = cmp.Or(o.RetryMin, time.Second)
	o.RetryMax = cmp.Or(o.RetryMax, time.Minute)
	return &Reconciler{
		opts:            o,
		dev:             dev,
		eps:             eps,
		store:           store,
		stateStore:      stateStore,
		secretLinks:     secretLinks,
		changed:         make(chan struct{}, 1),
		synced:          make(chan struct{}),
		status:          Status{FailedTenants: map[string]string{}},
		posted:          map[string]postedTenant{},
		owned:           map[string]bool{},
		incompleteSince: map[string]time.Time{},
		failures:        map[string]error{},
		vipTenant:       map[string]string{},
		notes:           map[noteKey][]string{},
		warned:          map[as3.ResourceKey]bool{},
		errored:         map[as3.ResourceKey]bool{},
		adding:          map[as3.ResourceKey]bool{},
		retryDelay:      o.RetryMin,
	}
}

// Changed tells the stores changed. It never blocks: changes coming while a pass is due are merged into it.
func (r *Reconciler) Changed() {
	select {
	case r.changed <- struct{}{}:
	default:
	}
}

// Synced tells the controller delivered every resource type once. The sweep waits for it: before, the stores are
// empty and every tenant of the instance would look orphan.
func (r *Reconciler) Synced() {
	r.syncedOnce.Do(func() { close(r.synced) })
}

func (r *Reconciler) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{FailedTenants: maps.Clone(r.status.FailedTenants), PostedVIPs: slices.Clone(r.status.PostedVIPs)}
}

// PoolsOfBackend gives the pools posted for a backend, one per VIP using it.
func (r *Reconciler) PoolsOfBackend(uuid string) []as3.PoolRef {
	r.mu.Lock()
	defer r.mu.Unlock()
	var pools []as3.PoolRef
	for _, p := range r.pools {
		if p.BackendUUID == uuid {
			pools = append(pools, p)
		}
	}
	return pools
}

// Run is the only goroutine talking to the device, it returns when ctx is done.
func (r *Reconciler) Run(ctx context.Context) error {
	debounce := stoppedTimer()
	maxDebounce := stoppedTimer()
	wake := stoppedTimer()
	debouncing := false
	resync := time.NewTicker(r.opts.ResyncInterval)
	defer resync.Stop()
	garbage := time.NewTimer(r.opts.GarbageAfterStart)
	defer garbage.Stop()
	garbageReady := false
	synced := r.synced
	isSynced := false

	for {
		pass := passOptions{}
		select {
		case <-ctx.Done():
			return nil
		case <-r.changed:
			debounce.Reset(r.opts.Debounce)
			if !debouncing {
				debouncing = true
				maxDebounce.Reset(r.opts.MaxDebounce)
			}
			continue
		case <-debounce.C:
		case <-maxDebounce.C:
		case <-wake.C:
		case <-resync.C:
			pass.resync = true
			pass.sweep = garbageReady && isSynced
		case <-garbage.C:
			garbageReady = true
			if !isSynced {
				continue
			}
			pass.sweep = true
		case <-synced:
			synced = nil
			isSynced = true
			if !garbageReady {
				continue
			}
			pass.sweep = true
		}
		debounce.Stop()
		maxDebounce.Stop()
		debouncing = false
		next, err := r.pass(ctx, pass)
		if err != nil {
			// only a stop ends a pass early, what is left is done by the next start
			return nil
		}
		wake.Stop()
		if !next.IsZero() {
			wake.Reset(time.Until(next))
		}
	}
}

func stoppedTimer() *time.Timer {
	t := time.NewTimer(time.Hour)
	t.Stop()
	return t
}

type passOptions struct {
	// resync posts every tenant, even the ones whose declaration didn't change
	resync bool
	// sweep deletes the tenants of this instance that no VIP uses, the ones posted before a restart included
	sweep bool
}

// pass brings the device to the stores. It gives when the next pass must run, zero when only a change needs one. It
// stops between tenants once ctx ends and gives its error, because a stop would otherwise wait for every tenant left,
// each failing on its cancelled request.
func (r *Reconciler) pass(ctx context.Context, opts passOptions) (time.Time, error) {
	entry := logging.Component("f5_reconciler")
	tenants, invalid, err := r.desired(ctx)
	if err != nil {
		entry.Error().Err(err).Msg("can't render the tenants")
		return time.Now().Add(r.opts.RetryMax), nil
	}
	var next time.Time
	retry := false
	for _, name := range r.deletionOrder(tenants) {
		if ctx.Err() != nil {
			return time.Time{}, ctx.Err()
		}
		err := r.dev.DeleteTenant(ctx, name)
		if err != nil {
			entry.Error().Err(err).Str("tenant", name).Msg("can't delete the tenant")
			retry = true
			continue
		}
		entry.Info().Str("tenant", name).Msg("tenant deleted, it has no VIP left")
		delete(r.posted, name)
		delete(r.owned, name)
	}
	for _, t := range r.postOrder(tenants) {
		if ctx.Err() != nil {
			return time.Time{}, ctx.Err()
		}
		outcome := r.reconcileTenant(ctx, t, opts.resync)
		retry = retry || outcome.retry
		next = earliest(next, outcome.wait)
	}
	if opts.sweep {
		retry = r.sweep(ctx, tenants) || retry
	}
	if ctx.Err() != nil {
		return time.Time{}, ctx.Err()
	}

	r.publish(ctx, tenants, invalid)
	if retry {
		retriesTotal.Inc()
		next = earliest(next, time.Now().Add(r.retryDelay))
		r.retryDelay = min(r.retryDelay*2, r.opts.RetryMax)
	} else {
		r.retryDelay = r.opts.RetryMin
	}
	return next, nil
}

// deletionOrder gives the tenants posted that have no VIP left. They go before any post: a VIP moving to another
// partition must leave the old one before its address can be declared in the new one.
func (r *Reconciler) deletionOrder(tenants map[string]*tenantRender) []string {
	var names []string
	for name := range r.posted {
		if tenants[name] == nil {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// postOrder puts first the tenants that lost a VIP, for the same reason as deletionOrder, then the others by name.
func (r *Reconciler) postOrder(tenants map[string]*tenantRender) []*tenantRender {
	ordered := slices.Collect(maps.Values(tenants))
	lostVIP := func(t *tenantRender) bool {
		for _, v := range r.posted[t.name].vips {
			if !slices.Contains(t.vipUUIDs(), v.GetUUID()) {
				return true
			}
		}
		return false
	}
	slices.SortFunc(ordered, func(a, b *tenantRender) int {
		aFirst, bFirst := lostVIP(a), lostVIP(b)
		if aFirst != bFirst {
			if aFirst {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.name, b.name)
	})
	return ordered
}

type tenantOutcome struct {
	retry bool
	// wait is when the tenant must be looked at again, zero when there is no need
	wait time.Time
}

func (r *Reconciler) reconcileTenant(ctx context.Context, t *tenantRender, resync bool) tenantOutcome {
	entry := logging.Component("f5_reconciler")
	if len(t.result.Missing) > 0 {
		since, waiting := r.incompleteSince[t.name]
		if !waiting {
			since = time.Now()
			r.incompleteSince[t.name] = since
		}
		deadline := since.Add(r.opts.MaxIncomplete)
		if time.Now().Before(deadline) {
			entry.Debug().Str("tenant", t.name).Msg("tenant waits for the resources it references")
			return tenantOutcome{wait: deadline}
		}
	} else {
		delete(r.incompleteSince, t.name)
	}

	last, wasPosted := r.posted[t.name]
	unchanged := wasPosted && bytes.Equal(last.doc, t.doc)
	if unchanged && !resync {
		return tenantOutcome{}
	}
	if holder := r.otherHolder(t); holder != "" {
		entry.Debug().Str("tenant", t.name).Str("holder", holder).Msg("tenant waits for a VIP to leave another partition")
		return tenantOutcome{retry: true}
	}

	owned, err := r.ownTenant(ctx, t.name)
	if err != nil {
		entry.Error().Err(err).Str("tenant", t.name).Msg("can't check who owns the partition")
		return tenantOutcome{retry: true}
	}
	if !owned {
		r.failTenant(t, fmt.Errorf("partition %s exists and is not managed by this flock instance", t.name))
		return tenantOutcome{}
	}

	started := time.Now()
	result, err := r.dev.Declare(ctx, t.name, t.doc)
	declareDuration.Observe(time.Since(started).Seconds())
	var bigipErr *bigip.Error
	if errors.As(err, &bigipErr) && bigipErr.Retryable() {
		declareTotal.WithLabelValues(t.name, "retry").Inc()
		entry.Warn().Err(err).Str("tenant", t.name).Msg("device not ready, the tenant is posted again later")
		return tenantOutcome{retry: true}
	}
	if err != nil {
		declareTotal.WithLabelValues(t.name, "refused").Inc()
		entry.Error().Err(err).Str("tenant", t.name).Msg("the device refused the tenant")
		r.posted[t.name] = postedTenant{doc: t.doc, vips: last.vips, pools: last.pools, failed: true}
		r.failTenant(t, err)
		return tenantOutcome{}
	}
	declareTotal.WithLabelValues(t.name, "success").Inc()
	entry.Info().Str("tenant", t.name).Bool("changed", result.Changed).Msg("tenant posted")

	r.writeNotes(ctx, t, unchanged && resync && result.Changed)
	r.posted[t.name] = postedTenant{doc: t.doc, vips: r.unresolvedVIPs(t.vips), pools: t.result.Pools}
	r.succeedTenant(t)
	return tenantOutcome{}
}

// otherHolder gives another tenant the device still holds a VIP of t in, empty when none. AS3 refuses an address
// declared in two partitions, so a moved VIP waits for the tenant it leaves.
func (r *Reconciler) otherHolder(t *tenantRender) string {
	for name, posted := range r.posted {
		if name == t.name {
			continue
		}
		for _, v := range posted.vips {
			if slices.Contains(t.vipUUIDs(), v.GetUUID()) {
				return name
			}
		}
	}
	return ""
}

// sweep deletes the tenants carrying the remark of this instance that no VIP uses. It tells a retry is needed.
func (r *Reconciler) sweep(ctx context.Context, tenants map[string]*tenantRender) bool {
	entry := logging.Component("f5_reconciler")
	onDevice, err := r.dev.ListTenants(ctx)
	if err != nil {
		entry.Error().Err(err).Msg("can't list the tenants to sweep")
		return true
	}
	retry := false
	for name, remark := range onDevice {
		if ctx.Err() != nil {
			return true
		}
		orphan := remark == as3.TenantRemark(r.opts.InstanceUUID) && tenants[name] == nil
		if !orphan {
			continue
		}
		if err := r.dev.DeleteTenant(ctx, name); err != nil {
			entry.Error().Err(err).Str("tenant", name).Msg("can't delete an orphan tenant")
			retry = true
			continue
		}
		entry.Info().Str("tenant", name).Msg("orphan tenant deleted")
		delete(r.posted, name)
		delete(r.owned, name)
	}
	return retry
}

func earliest(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}

// unresolvedVIPs gives the VIPs with their secret references back. The posted VIPs reach the agent status long after
// the post, when the links may no longer be the ones that resolved them.
func (r *Reconciler) unresolvedVIPs(vips []*vip.VIP) []*vip.VIP {
	unresolved := make([]*vip.VIP, 0, len(vips))
	for _, v := range vips {
		unresolved = append(unresolved, r.secretLinks.Unresolved(v).(*vip.VIP))
	}
	return unresolved
}
