package reconciler

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

const (
	notesSource       = "f5"
	approximationCode = "f5.approximation"
	postCode          = "f5.post"
)

var (
	vipTypeUrl     = helpers.TypeUrl[*vip.VIP]()
	vsTypeUrl      = helpers.TypeUrl[*vs.VirtualServer]()
	backendTypeUrl = helpers.TypeUrl[*backend.Backend]()
)

type noteKey struct {
	tenant   string
	resource as3.ResourceKey
}

func (r *Reconciler) failTenant(t *tenantRender, err error) {
	r.failures[t.name] = err
}

func (r *Reconciler) succeedTenant(t *tenantRender) {
	delete(r.failures, t.name)
	for _, v := range t.vips {
		r.vipTenant[v.GetUUID()] = t.name
	}
}

// writeNotes replaces the notes of a tenant just posted: the VIPs that moved from another partition, the references
// it was posted without, and the drift the post corrected.
func (r *Reconciler) writeNotes(ctx context.Context, t *tenantRender, drift bool) {
	for key := range r.notes {
		if key.tenant == t.name {
			delete(r.notes, key)
		}
	}
	note := func(resource as3.ResourceKey, text string) {
		key := noteKey{tenant: t.name, resource: resource}
		r.notes[key] = append(r.notes[key], text)
	}
	for _, v := range t.vips {
		vipKey := as3.KeyOf(v)
		previous := r.vipTenant[v.GetUUID()]
		if previous != "" && previous != t.name {
			note(vipKey, fmt.Sprintf("moved from partition %s, traffic was interrupted during the move", previous))
		}
		if drift {
			note(vipKey, "configuration drift corrected on the device")
		}
	}
	if drift {
		driftCorrections.Inc()
	}
	for from, refs := range t.result.Missing {
		for _, ref := range refs {
			note(from, r.missingNote(ctx, from, ref))
		}
	}
}

func (r *Reconciler) missingNote(ctx context.Context, from, ref as3.ResourceKey) string {
	if ref.TypeUrl == backendTypeUrl {
		known, _ := r.store.Backends().Exists(ctx, ref.UUID)
		if known {
			return fmt.Sprintf("endpoints of backend %s are not discovered yet, the %s is posted without them", ref.UUID, kindName(from.TypeUrl))
		}
	}
	return fmt.Sprintf("%s %s is not known yet, the %s is posted without it", kindName(ref.TypeUrl), ref.UUID, kindName(from.TypeUrl))
}

func kindName(typeUrl string) string {
	switch typeUrl {
	case vipTypeUrl:
		return "VIP"
	case vsTypeUrl:
		return "virtual server"
	case backendTypeUrl:
		return "backend"
	case helpers.TypeUrl[*cert.Certificate]():
		return "certificate"
	default:
		return typeUrl
	}
}

// publish writes what the pass found to the state store and the status: the approximations of every rendered resource
// and the notes of the last posts, the errors of the resources left out or refused, and clears what the agent set
// before and is gone now.
func (r *Reconciler) publish(ctx context.Context, tenants map[string]*tenantRender, invalid map[string]error) {
	r.forgetGoneTenants(tenants)
	notes, errs := r.collectProblems(tenants, invalid)
	r.publishNotes(notes)
	r.publishErrors(ctx, errs)
	r.publishAdding(ctx, tenants, errs)
	r.publishStatus()
}

func (r *Reconciler) forgetGoneTenants(tenants map[string]*tenantRender) {
	for name := range r.failures {
		if tenants[name] == nil {
			delete(r.failures, name)
		}
	}
	for key := range r.notes {
		if tenants[key.tenant] == nil {
			delete(r.notes, key)
		}
	}
}

// collectProblems gives the notes and errors of every resource: the approximations and refusals of the render, the
// failure of a tenant's last post on all its VIPs and virtual servers, and the notes of the posts.
func (r *Reconciler) collectProblems(
	tenants map[string]*tenantRender, invalid map[string]error,
) (map[as3.ResourceKey][]*flocktype.Note, map[as3.ResourceKey]error) {
	notes := map[as3.ResourceKey][]*flocktype.Note{}
	errs := map[as3.ResourceKey]error{}
	for vipUUID, err := range invalid {
		errs[as3.ResourceKey{TypeUrl: vipTypeUrl, UUID: vipUUID}] = err
	}
	for _, t := range tenants {
		for key, keyWarnings := range t.result.Warnings {
			for _, text := range keyWarnings {
				notes[key] = append(notes[key], f5Note(approximationCode, text))
			}
		}
		for key, err := range t.result.Errors {
			errs[key] = fmt.Errorf("%s", err)
		}
		failure := r.failures[t.name]
		if failure == nil {
			continue
		}
		for _, v := range t.vips {
			errs[as3.KeyOf(v)] = failure
			for _, vsUUID := range v.GetVirtualServersUuid() {
				errs[as3.ResourceKey{TypeUrl: vsTypeUrl, UUID: vsUUID}] = failure
			}
		}
	}
	for key, texts := range r.notes {
		for _, text := range texts {
			notes[key.resource] = append(notes[key.resource], f5Note(postCode, text))
		}
	}
	return notes, errs
}

// publishNotes sets the notes of every resource and clears those of the resources that had some at the last pass.
func (r *Reconciler) publishNotes(notes map[as3.ResourceKey][]*flocktype.Note) {
	for key, keyNotes := range notes {
		r.stateStore.SetNotes(key.TypeUrl, key.UUID, notesSource, sortedUniqueNotes(keyNotes))
	}
	for key := range r.warned {
		if _, still := notes[key]; !still {
			r.stateStore.SetNotes(key.TypeUrl, key.UUID, notesSource, nil)
		}
	}
	r.warned = map[as3.ResourceKey]bool{}
	for key := range notes {
		r.warned[key] = true
	}
}

// publishErrors sets the errors of every resource and clears the state of the resources that had one at the last pass.
func (r *Reconciler) publishErrors(ctx context.Context, errs map[as3.ResourceKey]error) {
	for key, err := range errs {
		if m := r.resourceOf(ctx, key); m != nil {
			r.stateStore.AddError(m, err)
		}
	}
	for key := range r.errored {
		if _, still := errs[key]; !still {
			r.stateStore.ClearState(key.TypeUrl, key.UUID)
		}
	}
	r.errored = map[as3.ResourceKey]bool{}
	for key := range errs {
		r.errored[key] = true
	}
}

func (r *Reconciler) publishAdding(
	ctx context.Context, tenants map[string]*tenantRender, errs map[as3.ResourceKey]error,
) {
	// the resources of a tenant not on the device yet (waiting, retrying, blocked) are ADDING, an error wins
	adding := map[as3.ResourceKey]bool{}
	for _, t := range tenants {
		last, wasPosted := r.posted[t.name]
		if wasPosted && bytes.Equal(last.doc, t.doc) {
			continue
		}
		for _, v := range t.vips {
			adding[as3.KeyOf(v)] = true
			for _, vsUUID := range v.GetVirtualServersUuid() {
				adding[as3.ResourceKey{TypeUrl: vsTypeUrl, UUID: vsUUID}] = true
			}
		}
	}
	for key := range adding {
		_, isErrored := errs[key]
		m := r.resourceOf(ctx, key)
		if isErrored || m == nil {
			delete(adding, key)
			continue
		}
		r.stateStore.SetState(m, agentstatus.StatusResponse_ADDING)
	}
	for key := range r.adding {
		_, isErrored := errs[key]
		if !adding[key] && !isErrored {
			r.stateStore.ClearState(key.TypeUrl, key.UUID)
		}
	}
	r.adding = adding
}

func (r *Reconciler) publishStatus() {
	status := Status{FailedTenants: map[string]string{}}
	for name, err := range r.failures {
		status.FailedTenants[name] = err.Error()
	}
	var pools []as3.PoolRef
	for _, name := range slices.Sorted(maps.Keys(r.posted)) {
		posted := r.posted[name]
		if posted.failed {
			continue
		}
		status.PostedVIPs = append(status.PostedVIPs, posted.vips...)
		pools = append(pools, posted.pools...)
	}
	tenantsGauge.Set(float64(len(r.posted)))

	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
	r.pools = pools
}

// resourceOf reads a resource from the stores, the state store keeps it with its error. Nil when it is gone.
func (r *Reconciler) resourceOf(ctx context.Context, key as3.ResourceKey) proto.Message {
	var m proto.Message
	var err error
	switch key.TypeUrl {
	case vipTypeUrl:
		m, err = r.store.VIPs().Read(ctx, key.UUID)
	case vsTypeUrl:
		m, err = r.store.VirtualServers().Read(ctx, key.UUID)
	case backendTypeUrl:
		m, err = r.store.Backends().Read(ctx, key.UUID)
	case helpers.TypeUrl[*cert.Certificate]():
		m, err = r.store.Certificates().Read(ctx, key.UUID)
	}
	if err != nil {
		return nil
	}
	return m
}

func f5Note(code, text string) *flocktype.Note {
	return &flocktype.Note{Level: flocktype.Note_WARNING, Code: code, Message: text}
}

// sortedUniqueNotes keeps the status stable from one pass to the next, and a text given by several tenants once
func sortedUniqueNotes(notes []*flocktype.Note) []*flocktype.Note {
	slices.SortFunc(notes, func(a, b *flocktype.Note) int {
		return cmp.Or(strings.Compare(a.GetCode(), b.GetCode()), strings.Compare(a.GetMessage(), b.GetMessage()))
	})
	return slices.CompactFunc(notes, func(a, b *flocktype.Note) bool {
		return a.GetCode() == b.GetCode() && a.GetMessage() == b.GetMessage()
	})
}
