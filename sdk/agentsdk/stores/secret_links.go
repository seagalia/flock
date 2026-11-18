package stores

import (
	"context"

	"cmp"
	"slices"
	"sync"

	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/proto"
)

// ResourceKey names one resource held by the agent.
type ResourceKey struct {
	TypeUrl string
	UUID    string
}

type secretLinkKey struct {
	typeUrl string
	uuid    string
	path    string
}

// SecretLinks keeps the secret references of the resources the agent holds, by their path in the resource. The stored
// resources carry the resolved values, so this is the only place left that knows which value came from which secret.
type SecretLinks struct {
	mutex sync.RWMutex
	refs  map[secretLinkKey]secret.Ref
}

func NewSecretLinks() *SecretLinks {
	return &SecretLinks{refs: map[secretLinkKey]secret.Ref{}}
}

// Set replaces every link of a resource, so a path given by the position of a header in a list stays right when the
// list changes.
func (l *SecretLinks) Set(typeUrl, uuid string, refs []secret.FoundRef) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.deleteLocked(typeUrl, uuid)
	for _, found := range refs {
		l.refs[secretLinkKey{typeUrl: typeUrl, uuid: uuid, path: found.Path}] = found.Ref
	}
}

func (l *SecretLinks) Delete(typeUrl, uuid string) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.deleteLocked(typeUrl, uuid)
}

func (l *SecretLinks) deleteLocked(typeUrl, uuid string) {
	for key := range l.refs {
		if key.typeUrl == typeUrl && key.uuid == uuid {
			delete(l.refs, key)
		}
	}
}

// Refs gives the links of a resource sorted by path.
func (l *SecretLinks) Refs(typeUrl, uuid string) []secret.FoundRef {
	l.mutex.RLock()
	defer l.mutex.RUnlock()
	refs := make([]secret.FoundRef, 0)
	for key, ref := range l.refs {
		if key.typeUrl == typeUrl && key.uuid == uuid {
			refs = append(refs, secret.FoundRef{Path: key.path, Ref: ref})
		}
	}
	slices.SortFunc(refs, func(a, b secret.FoundRef) int { return cmp.Compare(a.Path, b.Path) })
	return refs
}

// LinkedTo gives the resources pointing at a secret, each once, sorted so the re-runs keep the same order.
func (l *SecretLinks) LinkedTo(secretUUID string) []ResourceKey {
	l.mutex.RLock()
	defer l.mutex.RUnlock()
	linked := make([]ResourceKey, 0)
	for key, ref := range l.refs {
		if ref.SecretUUID == secretUUID {
			linked = append(linked, ResourceKey{TypeUrl: key.typeUrl, UUID: key.uuid})
		}
	}
	slices.SortFunc(linked, func(a, b ResourceKey) int {
		return cmp.Or(cmp.Compare(a.TypeUrl, b.TypeUrl), cmp.Compare(a.UUID, b.UUID))
	})
	return slices.Compact(linked)
}

// Unresolved gives the form of a message that may leave the agent, a secret without its values or any other message
// with its secret references back in place of the values.
func (l *SecretLinks) Unresolved(m proto.Message) proto.Message {
	if aSecret, isSecret := m.(*secret.Secret); isSecret {
		return aSecret.Blanked()
	}
	refs := l.Refs(helpers.TypeUrlFromMessage(m), helpers.GetIdentifier(m))
	if len(refs) == 0 {
		return m
	}
	unresolved := proto.Clone(m)
	secret.Unresolve(unresolved, refs)
	return unresolved
}

// SecretRefsOf gives the secret references of a message, in its metadata and its typed fields. A malformed one is
// left in the metadata as it came, since the controller validates the references before storing them.
func SecretRefsOf(m proto.Message) []secret.FoundRef {
	refs, _ := secret.FindRefs(m)
	return refs
}

// ResolveSecretRefs writes into m the values its references point at. A metadata reference to a secret the agent does
// not hold yet stays, a typed field gets an empty value until the secret arrives, a missing key of a held secret gives
// an empty value.
func ResolveSecretRefs(ctx context.Context, m proto.Message, refs []secret.FoundRef, secrets storers.Storer[*secret.Secret]) {
	secret.Resolve(m, refs, func(ref secret.Ref) (string, bool) {
		aSecret, err := secrets.Read(ctx, ref.SecretUUID)
		if err != nil {
			return "", false
		}
		return aSecret.GetData()[ref.Key], true
	})
}
