package adshandlers

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/seagalia/flock/sdk/agentsdk/aggregaters"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	oerrors "github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/storers"

	"google.golang.org/protobuf/proto"
)

const (
	// resourceTimeout bounds the work of the message handlers on one resource
	resourceTimeout = 10 * time.Second
	// removeDelay lets the additions of a response start before its removals
	removeDelay = 20 * time.Millisecond
	// firstRetryWait and maxRetryWait frame the wait between two failed aggregations
	firstRetryWait = 1 * time.Second
	maxRetryWait   = 30 * time.Second
)

type AdsHandler[T proto.Message] struct {
	entry           zerolog.Logger
	aggr            aggregaters.Aggregater
	chanCapacity    int
	messageHandlers []MessageHandler[T]
	store           storers.Storer[T]
	prevStore       storers.Storer[T]
	mutexRes        *sync.Map
	cleanWhenDone   bool
	stateStore      *stores.StateStore
	filters         svccore.ListFilters
	onSynced        func()
	secretLinks     *stores.SecretLinks
	secrets         storers.Storer[*secret.Secret]
}

// NewAdsHandler registers the handler of T in rt, whose stores it fills.
func NewAdsHandler[T proto.Message](
	rt *runtime.Runtime, aggr aggregaters.Aggregater, chanCapacity int,
	messageHandlers ...MessageHandler[T],
) *AdsHandler[T] {
	handler := &AdsHandler[T]{
		entry:           logging.Component("ads-handler").With().Str("type", helpers.TypeUrl[T]()).Logger(),
		aggr:            aggr,
		chanCapacity:    chanCapacity,
		messageHandlers: messageHandlers,
		store:           stores.StoreBy[T](rt.Store),
		prevStore:       stores.StoreBy[T](rt.PrevStore),
		mutexRes:        &sync.Map{},
		stateStore:      rt.States,
		secretLinks:     rt.SecretLinks,
		secrets:         rt.Store.Secrets(),
	}
	rt.RegisterHandler(handler)
	return handler
}

func (h *AdsHandler[T]) TypeUrl() string {
	return helpers.TypeUrl[T]()
}

func (h *AdsHandler[T]) SetCleanWhenDone(cleanWhenDone bool) {
	h.cleanWhenDone = cleanWhenDone
}

// SetOnSynced sets a function called once, after the first response of the controller was applied.
func (h *AdsHandler[T]) SetOnSynced(onSynced func()) {
	h.onSynced = onSynced
}

// WithFilters makes the controller only send the resources matching all the filters.
func (h *AdsHandler[T]) WithFilters(filters svccore.ListFilters) *AdsHandler[T] {
	h.filters = filters
	return h
}

func (h *AdsHandler[T]) lockRes(uuid string) {
	mutex, _ := h.mutexRes.LoadOrStore(uuid, &sync.Mutex{})
	mutex.(*sync.Mutex).Lock()
}

func (h *AdsHandler[T]) unlockRes(uuid string) {
	mutex, _ := h.mutexRes.LoadOrStore(uuid, &sync.Mutex{})
	mutex.(*sync.Mutex).Unlock()
}

// The versions are echoed back as the controller sent them, so the controller alone decides how a version is computed.
// A resource stored with no version from the controller gets an empty one, so the controller sends it again.
func (h *AdsHandler[T]) initResources(ctx context.Context) map[string]string {
	resources, err := h.store.List(ctx, nil)
	if err != nil {
		h.entry.Error().Msgf("Failed to list resources: %v", err)
		return nil
	}

	resourcesMap := make(map[string]string)
	for _, resource := range resources {
		uuid := helpers.GetIdentifier(resource)
		resourcesMap[uuid] = h.stateStore.Version(helpers.TypeUrl[T](), uuid)
	}
	return resourcesMap
}

func (h *AdsHandler[T]) Handle(ctx context.Context) error {
	entry := h.entry.With().Str("type", helpers.TypeUrl[T]()).Logger()
	chanReceive := make(chan *svcdiscovery.DeltaDiscoveryResponse, h.chanCapacity)
	chanReq := make(chan *svcdiscovery.DeltaDiscoveryRequest, h.chanCapacity)
	synced := false
	answered := func(toAnswer *svcdiscovery.DeltaDiscoveryRequest) {
		select {
		case chanReq <- toAnswer:
		case <-ctx.Done():
			return
		}
		if !synced && h.onSynced != nil {
			h.onSynced()
		}
		synced = true
	}
	aggregationDone := make(chan struct{})
	go func() {
		defer close(aggregationDone)
		waitTime := firstRetryWait
		for {
			entry.Info().Msg("Run aggregating ressources")
			initResources := func() map[string]string { return h.initResources(ctx) }
			err := h.aggr.DeltaAggregatedResources(ctx, helpers.TypeUrl[T](), h.filters, initResources, chanReceive, chanReq, func() {
				waitTime = firstRetryWait
			})
			if err == nil || ctx.Err() != nil {
				return
			}
			if strings.Contains(err.Error(), "connection refused") {
				waitTime = firstRetryWait
			}
			entry.Error().Msgf("Failed to aggregate resources, retry in %s: %v", waitTime.String(), err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(waitTime):
			}
			waitTime = min(waitTime*2, maxRetryWait)
		}
	}()
	for {
		select {
		case received := <-chanReceive:
			if received == nil {
				continue
			}
			// select picks at random among ready cases, a message buffered before the cancel must not start handlers
			if ctx.Err() != nil {
				continue
			}
			// direct response if there is no resource to update or delete
			if len(received.Resources) == 0 && len(received.RemovedResources) == 0 {
				toAnswer := &svcdiscovery.DeltaDiscoveryRequest{
					TypeUrl:       helpers.TypeUrl[T](),
					ResponseNonce: received.ResponseNonce,
				}
				answered(toAnswer)
				log.Debug().Msgf("Sent request back directly as there is no resource changes with: \n %s", toAnswer)
				continue
			}
			wg := &sync.WaitGroup{}
			wg.Add(len(received.Resources) + len(received.RemovedResources))
			for _, resource := range received.Resources {
				go func(wg *sync.WaitGroup, res *svcdiscovery.Resource) {
					defer wg.Done()
					resCtx, cancel := context.WithTimeout(ctx, resourceTimeout)
					defer cancel()
					err := h.AddResource(resCtx, res)
					if err != nil {
						entry.Error().
							Msgf("Failed to add resource '%s' with uuid '%s': %v", helpers.TypeUrl[T](), res.Uuid, err)
					}
				}(wg, resource)
			}
			// ensure that all resources are added before removing
			// by slowing down a little the removing process
			select {
			case <-time.After(removeDelay):
			case <-ctx.Done():
			}
			for _, uuid := range received.RemovedResources {
				go func(wg *sync.WaitGroup, uuid string) {
					defer wg.Done()
					resCtx, cancel := context.WithTimeout(ctx, resourceTimeout)
					defer cancel()
					err := h.RemoveResource(resCtx, uuid)
					if err != nil {
						entry.Error().Msgf("Failed to remove resource '%s' with uuid '%s': %v", helpers.TypeUrl[T](), uuid, err)
					}
				}(wg, uuid)
			}
			wg.Wait()
			toAnswer := &svcdiscovery.DeltaDiscoveryRequest{
				TypeUrl:       helpers.TypeUrl[T](),
				ResponseNonce: received.ResponseNonce,
			}
			answered(toAnswer)

		case <-ctx.Done():
			h.entry.Info().Msg("Stopping ADS handler ...")
			// the aggregation sends on channels this loop no longer reads, it must be over before the handlers stop
			<-aggregationDone
			var result error
			for _, hdl := range h.messageHandlers {
				if cleanHandler, ok := hdl.(Cleanable); ok && h.cleanWhenDone {
					err := cleanHandler.Clean()
					if err != nil {
						result = multierror.Append(result, fmt.Errorf("failed to clean handler for %s: %w", reflect.TypeOf(hdl), err))
					}
				}
				if stopHandler, ok := hdl.(Stoppable); ok {
					h.entry.Info().Msg("Stopping handler")
					err := stopHandler.Stop()
					if err != nil {
						result = multierror.Append(result, fmt.Errorf("failed to stop handler for %s: %w", reflect.TypeOf(hdl), err))
					}
				}
			}
			h.entry.Info().Msg("Finished Stopping ADS handler.")
			return nil
		}
	}
}

func (h *AdsHandler[T]) AddResource(ctx context.Context, res *svcdiscovery.Resource) error {
	typeUrl := helpers.TypeUrl[T]()

	h.entry.Info().Str("type", typeUrl).Msgf("Add/Update resource %s", res.Uuid)
	h.lockRes(res.Uuid)
	defer h.unlockRes(res.Uuid)
	var m T
	received := reflect.New(reflect.TypeOf(m).Elem()).Interface().(T)
	err := res.GetResource().UnmarshalTo(received)
	if err != nil {
		return err
	}
	prev := h.unresolvedStored(ctx, res.Uuid, received)
	// the links go in before resolving, so a secret stored meanwhile finds this resource and runs it again with its value
	oldRefs := h.secretLinks.Refs(typeUrl, res.Uuid)
	refs := stores.SecretRefsOf(received)
	h.secretLinks.Set(typeUrl, res.Uuid, refs)
	mess := proto.Clone(received).(T)
	stores.ResolveSecretRefs(ctx, mess, refs, h.secrets)

	err = h.storeResource(ctx, mess, prev)
	if err != nil {
		h.secretLinks.Set(typeUrl, res.Uuid, oldRefs)
		return err
	}
	h.stateStore.SetVersion(typeUrl, res.Uuid, res.GetSignature())
	stats.AddResource(typeUrl, res.Uuid, helpers.GetName(mess), res.GetSignature())
	return h.runAddHandlers(ctx, res.Uuid, mess)
}

// unresolvedStored gives the message the agent held until now with its secret references in place of the values, the
// one to keep in the prev store, since only the status answers read it and they leave the agent. It must run before
// the links change, they alone can put its references back.
func (h *AdsHandler[T]) unresolvedStored(ctx context.Context, uuid string, whenNew T) T {
	stored, err := h.store.Read(ctx, uuid)
	if err != nil {
		return whenNew
	}
	return h.secretLinks.Unresolved(stored).(T)
}

func (h *AdsHandler[T]) storeResource(ctx context.Context, mess, prev T) error {
	err := h.prevStore.Create(ctx, prev)
	if err != nil {
		return err
	}
	return h.store.Create(ctx, mess)
}

func (h *AdsHandler[T]) runAddHandlers(ctx context.Context, uuid string, mess T) error {
	typeUrl := helpers.TypeUrl[T]()
	if len(h.messageHandlers) == 0 {
		return nil
	}

	if len(h.messageHandlers) == 1 {
		h.stateStore.SetState(mess, agentstatus.StatusResponse_ADDING)
		err := h.messageHandlers[0].OnMessageAdded(ctx, mess)
		if err != nil {
			h.stateStore.AddError(mess, fmt.Errorf("failed to add: %w", err))
			stats.IncAddResourceFailure(h.messageHandlers[0].Name(), typeUrl)
			return err
		}
		stats.IncAddResourceSuccess(h.messageHandlers[0].Name(), typeUrl)
		h.stateStore.ClearState(typeUrl, uuid)
		return nil
	}
	errChan := make(chan error, len(h.messageHandlers))
	wg := &sync.WaitGroup{}
	wg.Add(len(h.messageHandlers))
	for _, hdl := range h.messageHandlers {
		go func(hdl MessageHandler[T]) {
			defer wg.Done()
			h.stateStore.SetState(mess, agentstatus.StatusResponse_ADDING)
			err := hdl.OnMessageAdded(ctx, mess)
			if err != nil {
				stats.IncAddResourceFailure(hdl.Name(), typeUrl)
				errChan <- fmt.Errorf("failed to add message for %s: %w", reflect.TypeOf(hdl), err)
				return
			}
			stats.IncAddResourceSuccess(hdl.Name(), typeUrl)
		}(hdl)
	}
	wg.Wait()
	close(errChan)
	var result error
	for err := range errChan {
		result = multierror.Append(result, err)
	}
	if result != nil {
		h.stateStore.AddError(mess, fmt.Errorf("failed to add: %w", result))
		return result
	}
	h.stateStore.ClearState(typeUrl, uuid)
	return nil
}

func (h *AdsHandler[T]) RemoveResource(ctx context.Context, uuid string) error {
	typeUrl := helpers.TypeUrl[T]()

	h.entry.Info().Str("type", typeUrl).Msgf("Remove resource %s", uuid)
	h.lockRes(uuid)
	defer h.unlockRes(uuid)
	mess, err := h.store.Read(ctx, uuid)
	if err != nil {
		if oerrors.IsErrNotFound(err) {
			return nil
		}
		return err
	}

	if len(h.messageHandlers) == 0 {
		return nil
	}
	if len(h.messageHandlers) == 1 {
		h.stateStore.SetState(mess, agentstatus.StatusResponse_REMOVING)
		err := h.messageHandlers[0].OnMessageRemove(ctx, mess)
		if err != nil {
			h.stateStore.AddError(mess, fmt.Errorf("failed to remove: %w", err))
			stats.IncRmResourceFailure(h.messageHandlers[0].Name(), typeUrl)
			return err
		}
		h.stateStore.DeleteTyped(typeUrl, uuid)

		stats.IncRmResourceSuccess(h.messageHandlers[0].Name(), typeUrl)

		h.store.Delete(ctx, uuid)     //nolint
		h.prevStore.Delete(ctx, uuid) // nolint
		h.secretLinks.Delete(typeUrl, uuid)
		stats.RemoveResource(typeUrl, uuid, helpers.GetName(mess))
		return nil
	}
	errChan := make(chan error, len(h.messageHandlers))
	wg := &sync.WaitGroup{}
	wg.Add(len(h.messageHandlers))
	for _, hdl := range h.messageHandlers {
		go func(hdl MessageHandler[T]) {
			defer wg.Done()
			h.stateStore.SetState(mess, agentstatus.StatusResponse_REMOVING)
			err := hdl.OnMessageRemove(ctx, mess)
			if err != nil {
				stats.IncRmResourceFailure(hdl.Name(), typeUrl)
				errChan <- fmt.Errorf("failed to remove message for %s: %w", reflect.TypeOf(hdl), err)
				return
			}
			stats.IncRmResourceSuccess(hdl.Name(), typeUrl)
		}(hdl)
	}
	wg.Wait()
	close(errChan)
	var result error
	for err := range errChan {
		result = multierror.Append(result, err)
	}
	if result != nil {
		h.stateStore.AddError(mess, fmt.Errorf("failed to remove: %w", result))
		return result
	}

	h.stateStore.DeleteTyped(typeUrl, uuid)

	h.store.Delete(ctx, uuid)     //nolint
	h.prevStore.Delete(ctx, uuid) // nolint
	h.secretLinks.Delete(typeUrl, uuid)
	stats.RemoveResource(typeUrl, uuid, helpers.GetName(mess))
	return nil
}

// Rerun resolves the stored message again from its secret references and gives it to the message handlers. The
// version stays the one the controller sent, because the resource itself did not change.
func (h *AdsHandler[T]) Rerun(ctx context.Context, uuid string) error {
	h.lockRes(uuid)
	defer h.unlockRes(uuid)
	stored, err := h.store.Read(ctx, uuid)
	if oerrors.IsErrNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	mess := proto.Clone(stored).(T)
	stores.ResolveSecretRefs(ctx, mess, h.secretLinks.Refs(helpers.TypeUrl[T](), uuid), h.secrets)

	err = h.storeResource(ctx, mess, h.secretLinks.Unresolved(stored).(T))
	if err != nil {
		return err
	}
	return h.runAddHandlers(ctx, uuid, mess)
}
