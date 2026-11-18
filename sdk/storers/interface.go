package storers

import (
	"context"
	"fmt"
	"time"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

const (
	FilterNameKey = "name"
	FilterUuidKey = "uuid"
)

type ListOptions struct {
	// Limit is the maximum number of items to return.
	Limit int
	// Offset is the number of items to skip before starting to collect the result set.
	Offset     int
	UserName   string
	UserGroups []string
}

type Storer[T proto.Message] interface {
	Create(ctx context.Context, message T) error
	Read(ctx context.Context, identifier string) (T, error)
	Update(ctx context.Context, identifier string, message T) error
	Delete(ctx context.Context, identifier string) error
	List(ctx context.Context, opt *ListOptions, filters ...*svccore.ListFilter) ([]T, error)
	Exists(ctx context.Context, identifier string) (bool, error)

	// FindForeignBySrc gives foreign type url -> foreign identifiers of the source identifier
	FindForeignBySrc(ctx context.Context, identifier string) (map[string][]string, error)
	// FindForeignResources gives source identifier -> foreign identifiers for the foreign typeUrl
	FindForeignResources(ctx context.Context, typeUrl string) (map[string][]string, error)
	// FindSrcWithForeignResource gives the source identifiers using the foreign resource identifier
	FindSrcWithForeignResource(ctx context.Context, typeUrl string, identifier string) ([]string, error)
}

type Foreigner interface {
	// ForeignResources gives foreign type url -> foreign identifiers
	ForeignResources() map[string][]string
}

type StorerFindName[T proto.Message] interface {
	FindByName(ctx context.Context, name string) (T, error)
}

type StoreManager interface {
	Backends() Storer[*backend.Backend]
	Instances() Storer[*instance.Instance]
	VIPs() Storer[*vip.VIP]
	VirtualServers() Storer[*vs.VirtualServer]
	Certificates() Storer[*cert.Certificate]
	Secrets() Storer[*secret.Secret]
	Zones() Storer[*zone.Zone]
	Nodes() NodeStore
}

// NodeHeldError is returned by ClaimNode when another address holds the node, Addr is that address
type NodeHeldError struct{ Addr string }

func (e *NodeHeldError) Error() string {
	return fmt.Sprintf("node held from %s", e.Addr)
}

// NodeStore keeps the nodes agents announce when they connect to a controller, one per agent and instance. The store is
// shared by the controllers, so any of them can list the nodes whichever one the agent is connected to, and two
// processes using the same agent identity from different addresses are detected across controllers.
type NodeStore interface {
	// ClaimNode records the node of the agent connecting from addr and refreshes its last seen time. It fails with a
	// *NodeHeldError when the row holds another address seen less than staleAfter ago.
	ClaimNode(ctx context.Context, instanceUuid string, node *svclookup.InstanceNode, addr string, staleAfter time.Duration) error
	// TouchNode refreshes the last seen time of the node when it is still held from addr, so a controller never keeps
	// fresh a node another address took over
	TouchNode(ctx context.Context, instanceUuid, agentUuid, addr string) error
	ListNodes(ctx context.Context, instanceUuid string) ([]*svclookup.InstanceNode, error)
	// FindNodeInstance gives the instance the agent last connected for, a not found store error when none
	FindNodeInstance(ctx context.Context, agentUuid string) (string, error)
	// DeleteNodes removes every node of the instance, once the instance itself is deleted.
	DeleteNodes(ctx context.Context, instanceUuid string) error
}

func StoreBy[T proto.Message](storeMan StoreManager) (Storer[T], error) {
	typeUrl := helpers.TypeUrl[T]()

	switch typeUrl {
	case helpers.TypeUrl[*backend.Backend]():
		return storeMan.Backends().(Storer[T]), nil
	case helpers.TypeUrl[*instance.Instance]():
		return storeMan.Instances().(Storer[T]), nil
	case helpers.TypeUrl[*vip.VIP]():
		return storeMan.VIPs().(Storer[T]), nil
	case helpers.TypeUrl[*vs.VirtualServer]():
		return storeMan.VirtualServers().(Storer[T]), nil
	case helpers.TypeUrl[*cert.Certificate]():
		return storeMan.Certificates().(Storer[T]), nil
	case helpers.TypeUrl[*secret.Secret]():
		return storeMan.Secrets().(Storer[T]), nil
	case helpers.TypeUrl[*zone.Zone]():
		return storeMan.Zones().(Storer[T]), nil
	default:
		return nil, fmt.Errorf("no store found for type %s", typeUrl)
	}
}
