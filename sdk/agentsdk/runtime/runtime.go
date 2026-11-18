// Package runtime holds the state of one agent app. Each agent app creates one in NewApp and gives it, or its parts, to
// the packages using them. The runtime holds the ads handlers of the app, each registers itself when built.
package runtime

import (
	"context"
	"slices"
	"sync"

	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/agentsdk/waiters"
	"github.com/seagalia/flock/sdk/storers"
)

// AdsHandler is the handler of one resource type, which *adshandlers.AdsHandler[T] is. The app starts the ones of its
// runtime, a secret change runs again the resources pointing at it through the handlers of their type.
type AdsHandler interface {
	TypeUrl() string
	Handle(ctx context.Context) error
	Rerun(ctx context.Context, uuid string) error
	SetCleanWhenDone(cleanWhenDone bool)
	SetOnSynced(onSynced func())
}

// Runtime is the state of one agent app. Two apps in one process each own one, so they never see each other's resources
// or handlers.
type Runtime struct {
	// Store holds the received resources, their secret references resolved
	Store storers.StoreManager
	// PrevStore holds the resources as the controller sent them, the ones the status answers show
	PrevStore   storers.StoreManager
	States      *stores.StateStore
	SecretLinks *stores.SecretLinks
	IpPorts     *stores.IpPortTranslator
	Waiter      waiters.Waiter

	handlersMutex sync.RWMutex
	// handlers are in registration order, the order the app starts them
	handlers []AdsHandler
}

func New() *Runtime {
	store := storers.NewMemoryManager()
	return &Runtime{
		Store:       store,
		PrevStore:   storers.NewMemoryManager(),
		States:      stores.NewStateStore(),
		SecretLinks: stores.NewSecretLinks(),
		IpPorts:     stores.NewIpPortTranslator(store),
		Waiter:      waiters.NewSuspendWaiter(),
	}
}

func (rt *Runtime) RegisterHandler(handler AdsHandler) {
	rt.handlersMutex.Lock()
	defer rt.handlersMutex.Unlock()
	rt.handlers = append(rt.handlers, handler)
}

// Handlers gives the handlers of typeUrl in registration order, all of them for an empty typeUrl.
func (rt *Runtime) Handlers(typeUrl string) []AdsHandler {
	rt.handlersMutex.RLock()
	defer rt.handlersMutex.RUnlock()
	if typeUrl == "" {
		return slices.Clone(rt.handlers)
	}
	var ofType []AdsHandler
	for _, handler := range rt.handlers {
		if handler.TypeUrl() == typeUrl {
			ofType = append(ofType, handler)
		}
	}
	return ofType
}
