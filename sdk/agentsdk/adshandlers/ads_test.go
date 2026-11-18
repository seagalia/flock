package adshandlers_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	. "github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"
)

var _ = Describe("AdsHandler", func() {
	var (
		mockCtrl    *gomock.Controller
		mockAggr    *testhelpers.MockAggregater
		mockHandler *testhelpers.MockMessageHandler[*backend.Backend]
		ctx         = context.Background()
		typeUrl     = helpers.TypeUrl[*backend.Backend]()
		aBackend    *backend.Backend
		errFailed   = fmt.Errorf("failed")
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockAggr = testhelpers.NewMockAggregater(mockCtrl)
		mockHandler = testhelpers.NewMockMessageHandler[*backend.Backend](mockCtrl)
		mockHandler.EXPECT().Name().Return("backend").AnyTimes()
		aBackend = makeBackend("backend-1")
	})

	Context("AddResource", func() {
		var handler *AdsHandler[*backend.Backend]

		BeforeEach(func() {
			handler = NewAdsHandler[*backend.Backend](rt, mockAggr, 1, mockHandler)
		})

		It("should store the resource and give it to the handler", func() {
			mockHandler.EXPECT().OnMessageAdded(ctx, ProtoEq(aBackend)).Return(nil)
			Expect(handler.AddResource(ctx, makeResource(aBackend, "backend-1"))).To(Succeed())

			stored, err := store.Backends().Read(context.Background(), "backend-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(stored, aBackend)).To(BeTrue())
		})

		It("should keep the previous version of an updated resource", func() {
			mockHandler.EXPECT().OnMessageAdded(ctx, gomock.Any()).Return(nil).Times(2)
			Expect(handler.AddResource(ctx, makeResource(aBackend, "backend-1"))).To(Succeed())

			updated := makeBackend("backend-1")
			updated.Name = "updated"
			Expect(handler.AddResource(ctx, makeResource(updated, "backend-1"))).To(Succeed())

			prev, err := prevStore.Backends().Read(context.Background(), "backend-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(prev.GetName()).To(Equal("backend-1"))
		})

		It("should record the error when the handler fails and clear it on next success", func() {
			mockHandler.EXPECT().OnMessageAdded(ctx, gomock.Any()).Return(errFailed)
			Expect(handler.AddResource(ctx, makeResource(aBackend, "backend-1"))).To(MatchError(errFailed))
			_, _, err := states.Get(typeUrl, "backend-1")
			Expect(err).To(MatchError(ContainSubstring("failed to add")))

			mockHandler.EXPECT().OnMessageAdded(ctx, gomock.Any()).Return(nil)
			Expect(handler.AddResource(ctx, makeResource(aBackend, "backend-1"))).To(Succeed())
			_, _, err = states.Get(typeUrl, "backend-1")
			Expect(err).ToNot(HaveOccurred())
		})

		It("should fail when the resource is not of the handler type", func() {
			Expect(handler.AddResource(ctx, makeResource(&svcdiscovery.Resource{}, "backend-1"))).ToNot(Succeed())
		})

		It("should give the resource to every handlers and merge their errors", func() {
			otherHandler := testhelpers.NewMockMessageHandler[*backend.Backend](mockCtrl)
			otherHandler.EXPECT().Name().Return("other").AnyTimes()
			handler = NewAdsHandler[*backend.Backend](rt, mockAggr, 1, mockHandler, otherHandler)

			mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(aBackend)).Return(nil)
			otherHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(aBackend)).Return(errFailed)
			Expect(handler.AddResource(ctx, makeResource(aBackend, "backend-1"))).To(MatchError(ContainSubstring("failed")))
		})
	})

	Context("RemoveResource", func() {
		var handler *AdsHandler[*backend.Backend]

		BeforeEach(func() {
			handler = NewAdsHandler[*backend.Backend](rt, mockAggr, 1, mockHandler)
		})

		It("should give the stored resource to the handler and remove it from stores", func() {
			mockHandler.EXPECT().OnMessageAdded(ctx, gomock.Any()).Return(nil)
			Expect(handler.AddResource(ctx, makeResource(aBackend, "backend-1"))).To(Succeed())

			mockHandler.EXPECT().OnMessageRemove(ctx, ProtoEq(aBackend)).Return(nil)
			Expect(handler.RemoveResource(ctx, "backend-1")).To(Succeed())

			exists, err := store.Backends().Exists(context.Background(), "backend-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(exists).To(BeFalse())
		})

		It("should do nothing when the resource is unknown", func() {
			Expect(handler.RemoveResource(ctx, "unknown")).To(Succeed())
		})

		It("should keep the resource stored when the handler fails", func() {
			mockHandler.EXPECT().OnMessageAdded(ctx, gomock.Any()).Return(nil)
			Expect(handler.AddResource(ctx, makeResource(aBackend, "backend-1"))).To(Succeed())

			mockHandler.EXPECT().OnMessageRemove(ctx, gomock.Any()).Return(errFailed)
			Expect(handler.RemoveResource(ctx, "backend-1")).To(MatchError(errFailed))

			exists, err := store.Backends().Exists(context.Background(), "backend-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(exists).To(BeTrue())
		})

		It("should fail without calling handlers when the store can't be read", func() {
			readErr := fmt.Errorf("store unavailable")
			failingStore := failingReadManager{MemoryManager: storers.NewMemoryManager(), readErr: readErr}
			failingRt := runtime.New()
			failingRt.Store = failingStore
			handler = NewAdsHandler[*backend.Backend](failingRt, mockAggr, 1, mockHandler)

			// no OnMessageRemove expectation: gomock fails the spec if it is called
			Expect(handler.RemoveResource(ctx, "backend-1")).To(MatchError(readErr))
		})
	})

	Context("initial resource versions", func() {
		var handler *AdsHandler[*backend.Backend]

		// initialVersions runs the handler until the aggregator asks for the versions the agent holds
		initialVersions := func() map[string]string {
			versions := make(chan map[string]string, 1)
			mockAggr.EXPECT().
				DeltaAggregatedResources(gomock.Any(), typeUrl, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, _ string, _ svccore.ListFilters, initResources func() map[string]string,
					_ chan *svcdiscovery.DeltaDiscoveryResponse, _ chan *svcdiscovery.DeltaDiscoveryRequest, _ func(),
				) error {
					versions <- initResources()
					<-ctx.Done()
					return nil
				})
			handleCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			go func() { _ = handler.Handle(handleCtx) }()

			var received map[string]string
			Eventually(versions).Should(Receive(&received))
			return received
		}

		BeforeEach(func() {
			handler = NewAdsHandler[*backend.Backend](rt, mockAggr, 1, mockHandler)
		})

		It("should send back the version the controller sent, whatever the resource content", func() {
			mockHandler.EXPECT().OnMessageAdded(ctx, gomock.Any()).Return(nil)
			resource := makeResource(aBackend, "backend-1")
			resource.Signature = "opaque-version"
			Expect(handler.AddResource(ctx, resource)).To(Succeed())

			Expect(initialVersions()).To(Equal(map[string]string{"backend-1": "opaque-version"}))
		})

		It("should forget the version of a removed resource", func() {
			mockHandler.EXPECT().OnMessageAdded(ctx, gomock.Any()).Return(nil)
			Expect(handler.AddResource(ctx, makeResource(aBackend, "backend-1"))).To(Succeed())
			mockHandler.EXPECT().OnMessageRemove(ctx, gomock.Any()).Return(nil)
			Expect(handler.RemoveResource(ctx, "backend-1")).To(Succeed())

			Expect(initialVersions()).To(BeEmpty())
		})

		It("should send an empty version for a stored resource the controller never sent", func() {
			Expect(store.Backends().Create(context.Background(), makeBackend("never-sent"))).To(Succeed())

			Expect(initialVersions()).To(Equal(map[string]string{"never-sent": ""}))
		})
	})

	Context("Handle", func() {
		It("should ask for its filters, apply received resources and acknowledge them with the response nonce", func() {
			handler := NewAdsHandler[*backend.Backend](rt, mockAggr, 1, mockHandler)
			filters := svccore.ListFilters{{Key: "team", Value: "web", IsMetadata: true}}
			handler.WithFilters(filters)
			acks := make(chan *svcdiscovery.DeltaDiscoveryRequest, 1)
			mockAggr.EXPECT().
				DeltaAggregatedResources(gomock.Any(), typeUrl, filters, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, _ string, _ svccore.ListFilters, _ func() map[string]string,
					receiveResp chan *svcdiscovery.DeltaDiscoveryResponse,
					responseChan chan *svcdiscovery.DeltaDiscoveryRequest, _ func(),
				) error {
					receiveResp <- &svcdiscovery.DeltaDiscoveryResponse{
						Resources:     []*svcdiscovery.Resource{makeResource(aBackend, "backend-1")},
						ResponseNonce: "nonce-1",
					}
					acks <- <-responseChan
					<-ctx.Done()
					return nil
				})
			mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(aBackend)).Return(nil)

			handleCtx, cancel := context.WithCancel(ctx)
			handleDone := make(chan error)
			go func() {
				handleDone <- handler.Handle(handleCtx)
			}()

			var ack *svcdiscovery.DeltaDiscoveryRequest
			Eventually(acks).Should(Receive(&ack))
			Expect(ack.GetResponseNonce()).To(Equal("nonce-1"))
			Expect(ack.GetTypeUrl()).To(Equal(typeUrl))

			cancel()
			Eventually(handleDone).Should(Receive(BeNil()))
		})

		It("should tell once it applied the first response, even an empty one", func() {
			handler := NewAdsHandler[*backend.Backend](rt, mockAggr, 1, mockHandler)
			syncs := make(chan struct{}, 2)
			handler.SetOnSynced(func() { syncs <- struct{}{} })
			mockAggr.EXPECT().
				DeltaAggregatedResources(gomock.Any(), typeUrl, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, _ string, _ svccore.ListFilters, _ func() map[string]string,
					receiveResp chan *svcdiscovery.DeltaDiscoveryResponse,
					responseChan chan *svcdiscovery.DeltaDiscoveryRequest, _ func(),
				) error {
					receiveResp <- &svcdiscovery.DeltaDiscoveryResponse{ResponseNonce: "nonce-1"}
					<-responseChan
					receiveResp <- &svcdiscovery.DeltaDiscoveryResponse{ResponseNonce: "nonce-2"}
					<-responseChan
					<-ctx.Done()
					return nil
				})

			handleCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			go func() { _ = handler.Handle(handleCtx) }()

			Eventually(syncs).Should(Receive())
			Consistently(syncs, "200ms").ShouldNot(Receive())
		})

		It("should cancel the context of a resource blocked in a handler when Handle's context ends", func() {
			handler := NewAdsHandler[*backend.Backend](rt, mockAggr, 1, mockHandler)
			mockAggr.EXPECT().
				DeltaAggregatedResources(gomock.Any(), typeUrl, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, _ string, _ svccore.ListFilters, _ func() map[string]string,
					receiveResp chan *svcdiscovery.DeltaDiscoveryResponse,
					_ chan *svcdiscovery.DeltaDiscoveryRequest, _ func(),
				) error {
					receiveResp <- &svcdiscovery.DeltaDiscoveryResponse{
						Resources:     []*svcdiscovery.Resource{makeResource(aBackend, "backend-1")},
						ResponseNonce: "nonce-1",
					}
					<-ctx.Done()
					return nil
				})
			blocked := make(chan struct{})
			handlerCtxErr := make(chan error, 1)
			mockHandler.EXPECT().OnMessageAdded(gomock.Any(), ProtoEq(aBackend)).
				DoAndReturn(func(ctx context.Context, _ *backend.Backend) error {
					close(blocked)
					select {
					case <-ctx.Done():
						handlerCtxErr <- ctx.Err()
					case <-time.After(5 * time.Second):
						handlerCtxErr <- nil
					}
					return nil
				})

			handleCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			go func() { _ = handler.Handle(handleCtx) }()
			Eventually(blocked).Should(BeClosed())

			cancel()
			Eventually(handlerCtxErr, 2*time.Second).Should(Receive(MatchError(context.Canceled)))
		})

		It("should return once cancelled, with no aggregation left running behind a retry wait", func() {
			handler := NewAdsHandler[*backend.Backend](rt, mockAggr, 1, mockHandler)
			var calls atomic.Int32
			mockAggr.EXPECT().
				DeltaAggregatedResources(gomock.Any(), typeUrl, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, string, svccore.ListFilters, func() map[string]string,
					chan *svcdiscovery.DeltaDiscoveryResponse, chan *svcdiscovery.DeltaDiscoveryRequest, func(),
				) error {
					calls.Add(1)
					return errors.New("server unavailable")
				}).AnyTimes()

			handleCtx, cancel := context.WithCancel(ctx)
			handleDone := make(chan error, 1)
			go func() { handleDone <- handler.Handle(handleCtx) }()
			Eventually(calls.Load).Should(BeEquivalentTo(1))

			cancel()
			Eventually(handleDone, time.Second).Should(Receive(BeNil()))
			// the retry wait is 1s, a loop that kept running would call the aggregator again after it
			Consistently(calls.Load, 1500*time.Millisecond).Should(BeEquivalentTo(1))
		})

		It("should retry an error whose text says the context was canceled while its own context is alive", func() {
			handler := NewAdsHandler[*backend.Backend](rt, mockAggr, 1, mockHandler)
			var calls atomic.Int32
			mockAggr.EXPECT().
				DeltaAggregatedResources(gomock.Any(), typeUrl, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(ctx context.Context, _ string, _ svccore.ListFilters, _ func() map[string]string,
					_ chan *svcdiscovery.DeltaDiscoveryResponse, _ chan *svcdiscovery.DeltaDiscoveryRequest, _ func(),
				) error {
					if calls.Add(1) == 1 {
						return errors.New("remote: context canceled")
					}
					<-ctx.Done()
					return nil
				}).AnyTimes()

			handleCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			go func() { _ = handler.Handle(handleCtx) }()

			Eventually(calls.Load, 5*time.Second).Should(BeEquivalentTo(2))
		})

		It("should clean and stop handlers when context is done", func() {
			cleanStopHandler := &cleanableStoppableHandler{MockMessageHandler: mockHandler}
			handler := NewAdsHandler[*backend.Backend](rt, mockAggr, 1, cleanStopHandler)
			handler.SetCleanWhenDone(true)
			mockAggr.EXPECT().DeltaAggregatedResources(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(nil).AnyTimes()

			handleCtx, cancel := context.WithCancel(ctx)
			cancel()
			Expect(handler.Handle(handleCtx)).To(Succeed())
			Expect(cleanStopHandler.cleaned).To(BeTrue())
			Expect(cleanStopHandler.stopped).To(BeTrue())
		})
	})
})

type cleanableStoppableHandler struct {
	*testhelpers.MockMessageHandler[*backend.Backend]
	cleaned bool
	stopped bool
}

func (h *cleanableStoppableHandler) Clean() error {
	h.cleaned = true
	return nil
}

func (h *cleanableStoppableHandler) Stop() error {
	h.stopped = true
	return nil
}
