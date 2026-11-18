package storers

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"reflect"
	"sync"

	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

type MemoryManager struct {
	backends       Storer[*backend.Backend]
	instances      Storer[*instance.Instance]
	vips           Storer[*vip.VIP]
	virtualServers Storer[*vs.VirtualServer]
	certificates   Storer[*cert.Certificate]
	secrets        Storer[*secret.Secret]
	zones          Storer[*zone.Zone]
	nodes          *memoryNodes
}

func NewMemoryManager() *MemoryManager {
	manager := &MemoryManager{}
	manager.backends = NewMemory[*backend.Backend]()
	manager.instances = NewMemory[*instance.Instance]()
	manager.vips = NewMemory[*vip.VIP]()
	manager.virtualServers = NewMemory[*vs.VirtualServer]()
	manager.certificates = NewMemory[*cert.Certificate]()
	manager.secrets = NewMemory[*secret.Secret]()
	manager.zones = NewMemory[*zone.Zone]()
	manager.nodes = &memoryNodes{nodes: map[nodeKey]*memoryNode{}}
	return manager
}

func (m *MemoryManager) Backends() Storer[*backend.Backend] {
	return m.backends
}

func (m *MemoryManager) Instances() Storer[*instance.Instance] {
	return m.instances
}

func (m *MemoryManager) VIPs() Storer[*vip.VIP] {
	return m.vips
}

func (m *MemoryManager) VirtualServers() Storer[*vs.VirtualServer] {
	return m.virtualServers
}

func (m *MemoryManager) Certificates() Storer[*cert.Certificate] {
	return m.certificates
}

func (m *MemoryManager) Secrets() Storer[*secret.Secret] {
	return m.secrets
}

func (m *MemoryManager) Zones() Storer[*zone.Zone] {
	return m.zones
}

func (m *MemoryManager) Nodes() NodeStore {
	return m.nodes
}

type nodeKey struct {
	instanceUuid string
	agentUuid    string
}

type memoryNode struct {
	node *svclookup.InstanceNode
	addr string
}

type memoryNodes struct {
	mu    sync.Mutex
	nodes map[nodeKey]*memoryNode
}

func (m *memoryNodes) ClaimNode(
	_ context.Context,
	instanceUuid string,
	node *svclookup.InstanceNode,
	addr string,
	staleAfter time.Duration,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := nodeKey{instanceUuid, node.GetAgentUuid()}
	current, exists := m.nodes[key]
	if exists {
		isOtherAddr := current.addr != addr
		isFresh := time.Since(current.node.GetLastSeen().AsTime()) < staleAfter
		if isOtherAddr && isFresh {
			return &NodeHeldError{Addr: current.addr}
		}
	}
	claimed := proto.Clone(node).(*svclookup.InstanceNode)
	claimed.LastSeen = timestamppb.Now()
	m.nodes[key] = &memoryNode{node: claimed, addr: addr}
	return nil
}

func (m *memoryNodes) TouchNode(_ context.Context, instanceUuid, agentUuid, addr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, exists := m.nodes[nodeKey{instanceUuid, agentUuid}]
	if exists && current.addr == addr {
		current.node.LastSeen = timestamppb.Now()
	}
	return nil
}

func (m *memoryNodes) ListNodes(_ context.Context, instanceUuid string) ([]*svclookup.InstanceNode, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []*svclookup.InstanceNode{}
	for key, current := range m.nodes {
		if key.instanceUuid == instanceUuid {
			result = append(result, proto.Clone(current.node).(*svclookup.InstanceNode))
		}
	}
	slices.SortFunc(result, func(a, b *svclookup.InstanceNode) int {
		return strings.Compare(a.GetName(), b.GetName())
	})
	return result, nil
}

func (m *memoryNodes) FindNodeInstance(_ context.Context, agentUuid string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *memoryNode
	instanceUuid := ""
	for key, current := range m.nodes {
		if key.agentUuid != agentUuid {
			continue
		}
		if latest == nil || current.node.GetLastSeen().AsTime().After(latest.node.GetLastSeen().AsTime()) {
			latest = current
			instanceUuid = key.instanceUuid
		}
	}
	if latest == nil {
		return "", errors.NewErrNotFound("node", agentUuid)
	}
	return instanceUuid, nil
}

func (m *memoryNodes) DeleteNodes(_ context.Context, instanceUuid string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.nodes {
		if key.instanceUuid == instanceUuid {
			delete(m.nodes, key)
		}
	}
	return nil
}

type Memory[T proto.Message] struct {
	descriptor      protoreflect.Descriptor
	memMap          *sync.Map
	isImplForeigner bool
}

func NewMemory[T proto.Message]() *Memory[T] {
	var m T
	elem := reflect.New(reflect.TypeOf(m).Elem()).Interface()
	_, isImplForeigner := elem.(Foreigner)
	return &Memory[T]{
		descriptor:      m.ProtoReflect().Descriptor(),
		memMap:          &sync.Map{},
		isImplForeigner: isImplForeigner,
	}
}

// cloneMessage keeps a stored message apart from its callers: a handler reading, changing then updating
// a message would change it for every other reader (like an announcer keeping the previous version).
func cloneMessage[T proto.Message](message T) T {
	return proto.Clone(message).(T)
}

func (s *Memory[T]) Create(_ context.Context, message T) error {
	s.memMap.Store(helpers.GetIdentifier(message), cloneMessage(message))
	return nil
}

func (s *Memory[T]) Read(_ context.Context, identifier string) (T, error) {
	var message T
	msgRaw, ok := s.memMap.Load(identifier)
	if !ok {
		return message, errors.NewErrNotFound(string(s.descriptor.FullName()), identifier)
	}
	return cloneMessage(msgRaw.(T)), nil
}

func (s *Memory[T]) Exists(_ context.Context, identifier string) (bool, error) {
	_, ok := s.memMap.Load(identifier)
	return ok, nil
}

func (s *Memory[T]) Update(_ context.Context, identifier string, message T) error {
	s.memMap.Store(identifier, cloneMessage(message))
	return nil
}

func (s *Memory[T]) Delete(_ context.Context, identifier string) error {
	s.memMap.Delete(identifier)
	return nil
}

func (s *Memory[T]) List(_ context.Context, opt *ListOptions, filters ...*svccore.ListFilter) ([]T, error) {
	messages := make([]T, 0)
	s.memMap.Range(func(key, value any) bool {
		if !matchFilters(value.(T), filters) {
			return true
		}

		messages = append(messages, cloneMessage(value.(T)))
		return true
	})
	return paginate(messages, opt), nil
}

func (s *Memory[T]) FindForeignResources(ctx context.Context, typeUrl string) (map[string][]string, error) {
	mapMatch := make(map[string][]string)
	if !s.isImplForeigner {
		return mapMatch, nil
	}
	listMessages, err := s.List(ctx, nil)
	if err != nil {
		return mapMatch, err
	}
	for _, message := range listMessages {
		var messInt any = message
		mapRes := messInt.(Foreigner).ForeignResources()
		if _, ok := mapRes[typeUrl]; !ok {
			continue
		}
		mapMatch[helpers.GetIdentifier(message)] = mapRes[typeUrl]
	}
	return mapMatch, nil
}

func (s *Memory[T]) FindSrcWithForeignResource(ctx context.Context, typeUrl string, identifier string) ([]string, error) {
	foreignRessources, err := s.FindForeignResources(ctx, typeUrl)
	if err != nil {
		return []string{}, err
	}
	foundSource := make([]string, 0)
	for source, foreignRessources := range foreignRessources {
		for _, foreignRessource := range foreignRessources {
			if foreignRessource == identifier {
				foundSource = append(foundSource, source)
			}
		}
	}
	return foundSource, nil
}

func (s *Memory[T]) FindForeignBySrc(ctx context.Context, identifier string) (map[string][]string, error) {
	mapMatch := make(map[string][]string)
	if !s.isImplForeigner {
		return mapMatch, nil
	}
	listMessages, err := s.List(ctx, nil)
	if err != nil {
		return mapMatch, err
	}
	var messInt any
	for _, message := range listMessages {
		if helpers.GetIdentifier(message) != identifier {
			continue
		}
		messInt = message
		break
	}
	if messInt == nil {
		return mapMatch, nil
	}
	mapRes := messInt.(Foreigner).ForeignResources()
	maps.Copy(mapMatch, mapRes)
	return mapMatch, nil
}
