package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/seagalia/flock/cli/resfile"
	"github.com/seagalia/flock/sdk/clients"
	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	vipv1 "github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	vsv1 "github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const inFlightWriteTimeout = 30 * time.Second

// applyPlan is what a resource file would do. It holds the writes, in the order they must be applied, and the
// resources that already match their document.
type applyPlan struct {
	Changes   []change
	Unchanged []string
}

// storedResource is a resource of the controller as the identity and reference lookups see it.
type storedResource interface {
	proto.Message
	GetUUID() string
}

// resourceKey identifies a document in a run, a name is only unique within its kind.
type resourceKey struct{ kind, name string }

// applyOrder is the order of the writes, because a virtual server needs its backends and a vip needs its virtual
// servers.
var applyOrder = []string{resfile.KindBackend, resfile.KindVs, resfile.KindVip}

// planApply compares the documents with the controller. The documents are cloned, the caller's ones are not changed.
func planApply(ctx context.Context, client *clients.Client, docs []resfile.Document) (applyPlan, error) {
	documents := make([]resfile.Document, len(docs))
	existing := make(map[resourceKey]storedResource, len(docs))
	inFiles := make(map[resourceKey]bool, len(docs))
	for i, doc := range docs {
		documents[i] = doc
		documents[i].Message = proto.Clone(doc.Message)

		key := resourceKey{doc.Kind, doc.Name()}
		inFiles[key] = true
		stored, err := findExisting(ctx, client, doc)
		if err != nil {
			return applyPlan{}, fmt.Errorf("%s document %d: %w", doc.File, doc.Index, err)
		}
		if stored != nil {
			existing[key] = stored
		}
	}

	var plan applyPlan
	for _, kind := range applyOrder {
		for _, doc := range documents {
			if doc.Kind != kind {
				continue
			}
			key := resourceKey{doc.Kind, doc.Name()}
			stored := existing[key]

			if err := resolveSecretRefs(ctx, client, doc.Message); err != nil {
				return applyPlan{}, fmt.Errorf("%s document %d: %w", doc.File, doc.Index, err)
			}
			if err := resolveRefs(ctx, client, doc, inFiles, existing); err != nil {
				return applyPlan{}, fmt.Errorf("%s document %d: %w", doc.File, doc.Index, err)
			}
			if stored == nil {
				plan.Changes = append(plan.Changes, change{Kind: kind, Name: key.name, Action: actionCreate, To: doc.Message})
				continue
			}

			if err := refuseLockedDocument(doc, stored); err != nil {
				return applyPlan{}, err
			}

			setUUID(doc.Message, stored.GetUUID())
			if sameResource(stored, doc.Message) {
				plan.Unchanged = append(plan.Unchanged, kind+" "+key.name)
				continue
			}
			plan.Changes = append(plan.Changes,
				change{Kind: kind, Name: key.name, Action: actionUpdate, From: stored, To: doc.Message})
		}
	}
	return plan, nil
}

// findExisting gives the resource a document targets. That is the one of its UUID, which must exist, or else the one
// with its name. Nil means the document creates.
func findExisting(ctx context.Context, client *clients.Client, doc resfile.Document) (storedResource, error) {
	if uuid := doc.UUID(); uuid != "" {
		stored, err := readByUUID(ctx, client, doc.Kind, uuid)
		if err != nil {
			return nil, fmt.Errorf("%s %q: uuid %s: %w", doc.Kind, doc.Name(), uuid, err)
		}
		if stored == nil {
			return nil, fmt.Errorf("%s %q: uuid %s: not found", doc.Kind, doc.Name(), uuid)
		}
		return stored, nil
	}
	return findOneByName(ctx, client, doc.Kind, doc.Name())
}

// findOneByName is nil when no resource has the name and an error when several have it, the store has no unique
// constraint on names.
func findOneByName(ctx context.Context, client *clients.Client, kind, name string) (storedResource, error) {
	found, err := listByName(ctx, client, kind, name)
	if err != nil {
		return nil, fmt.Errorf("%s %q: %w", kind, name, err)
	}
	if len(found) == 0 {
		return nil, nil
	}
	if len(found) == 1 {
		return found[0], nil
	}

	uuids := make([]string, len(found))
	for i, resource := range found {
		uuids[i] = resource.GetUUID()
	}
	return nil, fmt.Errorf("%s %q: %d %s have this name (%s), use its UUID in the document",
		kind, name, len(found), pluralOf(kind), strings.Join(uuids, ", "))
}

func pluralOf(kind string) string {
	if kind == resfile.KindVs {
		return "virtual servers"
	}
	return kind + "s"
}

// readByUUID is nil when the controller answers without a resource.
func readByUUID(ctx context.Context, client *clients.Client, kind, uuid string) (storedResource, error) {
	request := &svccore.UuidRequest{UUID: uuid}
	switch kind {
	case resfile.KindBackend:
		resp, err := client.Backend().ReadBackend(ctx, request)
		return nilIfAbsent(resp.GetBackend(), err)
	case resfile.KindVs:
		resp, err := client.VirtualServer().ReadVS(ctx, request)
		return nilIfAbsent(resp.GetVs(), err)
	case resfile.KindVip:
		resp, err := client.Vip().ReadVIP(ctx, request)
		return nilIfAbsent(resp.GetVip(), err)
	case resfile.KindCertificate:
		resp, err := client.Certificate().ReadCertificate(ctx, request)
		return nilIfAbsent(resp.GetCertificate(), err)
	}
	return nil, fmt.Errorf("unknown kind %q", kind)
}

// nilIfAbsent keeps a nil resource from becoming a non-nil interface holding a nil pointer.
func nilIfAbsent[T storedResource](resource T, err error) (storedResource, error) {
	if err != nil {
		return nil, err
	}
	if !resource.ProtoReflect().IsValid() {
		return nil, nil
	}
	return resource, nil
}

func listByName(ctx context.Context, client *clients.Client, kind, name string) ([]storedResource, error) {
	filters := []*svccore.ListFilter{{Key: "name", Value: name, Operator: svccore.ListFilter_EQUAL}}
	switch kind {
	case resfile.KindBackend:
		return listAll(func(offset int32) ([]storedResource, error) {
			resp, err := client.Backend().ListBackend(ctx, &svcbackend.ListBackendRequest{Limit: listPageSize, Offset: offset, Filters: filters})
			return asStored(resp.GetBackends()), err
		})
	case resfile.KindVs:
		return listAll(func(offset int32) ([]storedResource, error) {
			resp, err := client.VirtualServer().ListVS(ctx, &svcvs.ListVSRequest{Limit: listPageSize, Offset: offset, Filters: filters})
			return asStored(resp.GetVs()), err
		})
	case resfile.KindVip:
		return listAll(func(offset int32) ([]storedResource, error) {
			resp, err := client.Vip().ListVIP(ctx, &svcvip.ListVIPRequest{Limit: listPageSize, Offset: offset, Filters: filters})
			return asStored(resp.GetVip()), err
		})
	case resfile.KindCertificate:
		return listAll(func(offset int32) ([]storedResource, error) {
			resp, err := client.Certificate().ListCertificate(ctx, &svccert.ListCertificateRequest{Limit: listPageSize, Offset: offset, Filters: filters})
			return asStored(resp.GetCertificates()), err
		})
	}
	return nil, fmt.Errorf("unknown kind %q", kind)
}

func asStored[T storedResource](resources []T) []storedResource {
	stored := make([]storedResource, len(resources))
	for i, resource := range resources {
		stored[i] = resource
	}
	return stored
}

// resolveRefs replaces the `name:` values of a document by uuids. A reference to a document of the files is resolved
// only when that resource exists already, otherwise it stays `name:<n>` for applyChanges to resolve with the uuid of
// the creation. Resolving the existing ones is what lets an unchanged document compare equal to the stored one.
func resolveRefs(
	ctx context.Context, client *clients.Client, doc resfile.Document,
	inFiles map[resourceKey]bool, existing map[resourceKey]storedResource,
) error {
	for _, ref := range resfile.Refs(doc) {
		key := resourceKey{ref.Kind, ref.Name}
		if inFiles[key] {
			if stored := existing[key]; stored != nil {
				ref.Set(stored.GetUUID())
			}
			continue
		}

		stored, err := findOneByName(ctx, client, ref.Kind, ref.Name)
		if err != nil {
			return err
		}
		if stored == nil {
			return fmt.Errorf("%s %q: no %s has this name", ref.Kind, ref.Name, ref.Kind)
		}
		ref.Set(stored.GetUUID())
	}
	return nil
}

func setUUID(message proto.Message, uuid string) {
	reflected := message.ProtoReflect()
	field := reflected.Descriptor().Fields().ByName("UUID")
	reflected.Set(field, protoreflect.ValueOfString(uuid))
}

// applyChanges writes the changes in order and stops at the first failure with an *applyFailure. The `name:` values
// left by the plan are resolved with the uuids of the resources this run created. When ctx ends, the write in flight
// finishes and the failure lists the changes never sent, so the user knows what to apply again.
func applyChanges(ctx context.Context, client *clients.Client, changes []change, onApplied func(change)) error {
	created := map[resourceKey]string{}
	var applied []string

	for i, c := range changes {
		uuid, err := writeUnlessInterrupted(ctx, client, c, created)
		if err != nil {
			var notApplied []string
			for _, next := range changes[i+1:] {
				notApplied = append(notApplied, next.String())
			}
			return &applyFailure{Failed: c, Applied: applied, NotApplied: notApplied, Err: err}
		}
		if c.Action == actionCreate {
			created[resourceKey{c.Kind, c.Name}] = uuid
		}
		applied = append(applied, c.String())
		if onApplied != nil {
			onApplied(c)
		}
	}
	return nil
}

// writeUnlessInterrupted refuses to start a write once ctx has ended. A write already started runs to its end on a
// context that outlives ctx, because one cut halfway would leave the user guessing whether it reached the controller.
func writeUnlessInterrupted(
	ctx context.Context, client *clients.Client, c change, created map[resourceKey]string,
) (string, error) {
	if ctx.Err() != nil {
		return "", errInterrupted
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), inFlightWriteTimeout)
	defer cancel()
	return writeChange(writeCtx, client, c, created)
}

// writeChange gives the uuid of the written resource.
func writeChange(ctx context.Context, client *clients.Client, c change, created map[resourceKey]string) (string, error) {
	to := proto.Clone(c.To)
	for _, ref := range resfile.Refs(resfile.Document{Kind: c.Kind, Message: to}) {
		uuid, isCreated := created[resourceKey{ref.Kind, ref.Name}]
		if !isCreated {
			return "", fmt.Errorf("%s %q is not created by this run", ref.Kind, ref.Name)
		}
		ref.Set(uuid)
	}

	isCreate := c.Action == actionCreate
	switch resource := to.(type) {
	case *backendv1.Backend:
		if isCreate {
			resp, err := client.Backend().CreateBackend(ctx, &svcbackend.CreateBackendRequest{Backend: resource})
			return resp.GetBackend().GetUUID(), err
		}
		_, err := client.Backend().UpdateBackend(ctx, &svcbackend.UpdateBackendRequest{UUID: resource.UUID, Backend: resource})
		return resource.UUID, err
	case *vsv1.VirtualServer:
		if isCreate {
			resp, err := client.VirtualServer().CreateVS(ctx, &svcvs.CreateVSRequest{Vs: resource})
			return resp.GetVs().GetUUID(), err
		}
		_, err := client.VirtualServer().UpdateVS(ctx, &svcvs.UpdateVSRequest{UUID: resource.UUID, Vs: resource})
		return resource.UUID, err
	case *vipv1.VIP:
		if isCreate {
			resp, err := client.Vip().CreateVIP(ctx, &svcvip.CreateVIPRequest{Vip: resource})
			return resp.GetVip().GetUUID(), err
		}
		_, err := client.Vip().UpdateVIP(ctx, &svcvip.UpdateVIPRequest{UUID: resource.UUID, Vip: resource})
		return resource.UUID, err
	}
	return "", fmt.Errorf("can't write a %T", c.To)
}

// refuseLockedDocument refuses a document that targets a locked resource. Documents that carry the lock key are
// already refused by resfile.Check, so only the stored side is left to check. It runs while planning, so nothing is
// written when it fails.
func refuseLockedDocument(doc resfile.Document, stored storedResource) error {
	err := refuseLocked(doc.Kind, doc.Name(), helpers.GetMetadata(stored))
	if err != nil {
		return fmt.Errorf("%s document %d: %w", doc.File, doc.Index, err)
	}
	return nil
}
