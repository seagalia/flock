package serves_test

import (
	"context"
	"fmt"
	"io"
	"time"

	"connectrpc.com/connect/v2"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/controller/announcers"
	. "github.com/seagalia/flock/controller/announcers/mocks"
	"github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/controller/lockers"
	. "github.com/seagalia/flock/controller/serves"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"
	"go.uber.org/mock/gomock"
)

const (
	instanceId    = "instanceId"
	edgeAgentUuid = "5f1d8a52-3c1e-4a8e-9d55-6f1c2b7a9e01"
)

// edgeInstantiater declares the agent edge-1 for every instance
func edgeInstantiater() instantiaters.Instantiater {
	return instantiaters.NewStatic([]*config.AgentInstantiator{{UUID: edgeAgentUuid, Name: "edge-1", Type: "starling"}})
}

var _ = Describe("FlockServer", func() {
	var (
		storeMan   *storers.MemoryManager
		mockCtrl   *gomock.Controller
		mockAds    *adsServerMock
		mockLinker *MockLinker
		server     *FlockServer
		mockListen *MockListener
		doneEmit   chan *DoneEmit
		errEmit    chan *ErrEmit
		sent       []*svcdiscovery.DeltaDiscoveryResponse
		edgeCtx    context.Context
	)
	// configure makes server use the mocks of the spec
	configure := func(server *FlockServer) {
		server.SetListenFactory(func(ctx context.Context, ads DiscoveryStream, doneEmit chan *DoneEmit, emit chan *ErrEmit) announcers.Listener {
			return mockListen
		})
		server.SetDoneChanFactory(func() chan *DoneEmit {
			return doneEmit
		})
		server.SetErrEmitChanFactory(func() chan *ErrEmit {
			return errEmit
		})
		server.SetInitDiscoverer(func(ctx context.Context, entry zerolog.Logger,
			stream DiscoveryStream,
			waitInit chan struct{}, done chan *DoneEmit) func(changes []*svcdiscovery.Resource, removes []string) {
			return func(changes []*svcdiscovery.Resource, removes []string) {
				entry.Debug().Msg("init discoverer")
				waitInit <- struct{}{}
			}
		})
	}
	firstRequest := func() {
		mockAds.EXPECT().Receive().Return(&svcdiscovery.DeltaDiscoveryRequest{
			TypeUrl:      "type-url",
			InstanceUuid: instanceId,
		}, nil).Times(1)
	}
	BeforeEach(func() {
		storeMan = storers.NewMemoryManager()
		err := storeMan.Instances().Create(context.Background(), &instance.Instance{
			UUID: instanceId,
			Name: instanceId,
		})
		Expect(err).ToNot(HaveOccurred())
		doneEmit = make(chan *DoneEmit, 1)
		errEmit = make(chan *ErrEmit, 1)
		mockCtrl = gomock.NewController(GinkgoT())
		mockListen = NewMockListener(mockCtrl)
		mockAds = newAdsServerMock(mockCtrl)
		mockLinker = NewMockLinker(mockCtrl)
		server = NewFlockServer(context.Background(), nil, config.AdminConfig{}, mockLinker, storeMan, nil, nil, nil, edgeInstantiater(), nil, lockers.NewMemory(), nil)
		configure(server)
		edgeCtx = agentContext("edge-1")

		sent = nil
		mockAds.EXPECT().Send(gomock.Any()).DoAndReturn(func(resp *svcdiscovery.DeltaDiscoveryResponse) error {
			sent = append(sent, resp)
			return nil
		}).AnyTimes()

		mockLinker.EXPECT().Connect(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(instanceUuid string, typeUrl string, aListen announcers.Listener,
				initResVers map[string]string, filters svccore.ListFilters, init announcers.InitConnectFunc) error {
				init([]*svcdiscovery.Resource{}, []string{})
				return nil
			}).AnyTimes()
	})
	AfterEach(func() {
		mockCtrl.Finish()
	})
	Context("DeltaAggregatedResources", func() {
		It("should call listener and wait for complete", func() {
			// EOF is the error returned by the server when the client closes the connection and is not considered as an error by server
			mockListen.EXPECT().OnAnnounce(gomock.Any(), gomock.Any()).DoAndReturn(func(eventName announcers.EventName, resources []*svcdiscovery.Resource) {
				doneEmit <- &DoneEmit{Err: io.EOF}
			}).Times(1)
			mockAds.EXPECT().Receive().DoAndReturn(func() (*svcdiscovery.DeltaDiscoveryRequest, error) {
				return &svcdiscovery.DeltaDiscoveryRequest{
					TypeUrl:      "type-url",
					InstanceUuid: instanceId,
				}, nil
			}).Times(1)

			mockLinker.EXPECT().Disconnect(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
			done := make(chan struct{})

			go func() {
				defer GinkgoRecover()
				err := serveDiscovery(edgeCtx, server, mockAds)
				Expect(err).ToNot(HaveOccurred())
				close(done)
			}()
			mockListen.OnAnnounce(announcers.ChangeEvent, []*svcdiscovery.Resource{})
			Eventually(done).Should(BeClosed())

		})
		It("should refuse the sends of its listener once it returned", func() {
			var listenedStream DiscoveryStream
			server.SetListenFactory(func(ctx context.Context, ads DiscoveryStream, doneEmit chan *DoneEmit, emit chan *ErrEmit) announcers.Listener {
				listenedStream = ads
				return mockListen
			})
			mockAds.EXPECT().Receive().Return(&svcdiscovery.DeltaDiscoveryRequest{
				TypeUrl:      "type-url",
				InstanceUuid: instanceId,
			}, nil).Times(1)
			mockLinker.EXPECT().Disconnect(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
			doneEmit <- &DoneEmit{Err: io.EOF}

			Expect(serveDiscovery(edgeCtx, server, mockAds)).To(Succeed())

			// a write after the handler returned panics in net/http
			Expect(listenedStream.Send(&svcdiscovery.DeltaDiscoveryResponse{})).To(HaveOccurred())
			Expect(sent).To(HaveLen(1))
		})
		It("should return once its init has stopped waiting for the ack the agent never sent", func() {
			linker := NewMockLinker(mockCtrl)
			server = NewFlockServer(context.Background(), nil, config.AdminConfig{}, linker, storeMan, nil, nil, nil, edgeInstantiater(), nil, lockers.NewMemory(), nil)
			configure(server)
			initReturned := make(chan struct{})
			server.SetInitDiscoverer(func(ctx context.Context, entry zerolog.Logger,
				stream DiscoveryStream,
				waitInit chan struct{}, done chan *DoneEmit) func(changes []*svcdiscovery.Resource, removes []string) {
				init := DefaultInitDiscoverer(ctx, entry, stream, waitInit, done)
				return func(changes []*svcdiscovery.Resource, removes []string) {
					defer close(initReturned)
					init(changes, removes)
				}
			})
			linker.EXPECT().Connect(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(instanceUuid string, typeUrl string, aListen announcers.Listener,
					initResVers map[string]string, filters svccore.ListFilters, init announcers.InitConnectFunc) error {
					go init([]*svcdiscovery.Resource{{Uuid: "uuid"}}, nil)
					return nil
				}).Times(1)
			linker.EXPECT().Disconnect(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			firstRequest()
			ackAsked := make(chan struct{})
			releaseAck := make(chan struct{})
			DeferCleanup(func() { close(releaseAck) })
			mockAds.EXPECT().Receive().DoAndReturn(func() (*svcdiscovery.DeltaDiscoveryRequest, error) {
				close(ackAsked)
				<-releaseAck
				return nil, io.EOF
			}).Times(1)
			ctx, cancel := context.WithCancel(edgeCtx)
			handlerDone := make(chan error, 1)
			go func() { handlerDone <- serveDiscovery(ctx, server, mockAds) }()
			Eventually(ackAsked).Should(BeClosed())

			cancel()

			Eventually(handlerDone).WithTimeout(2 * time.Second).Should(Receive())
			// init sends on the stream, it must not outlive the handler
			Eventually(initReturned).WithTimeout(500 * time.Millisecond).Should(BeClosed())
		})
		It("should record the node of the first request with its extensions", func() {
			mockAds.EXPECT().Receive().Return(&svcdiscovery.DeltaDiscoveryRequest{
				TypeUrl:      "type-url",
				InstanceUuid: instanceId,
				Node: &core.Node{ClientType: "starling", Extensions: []*core.Extension{
					{TypeUrl: "type-1", Category: "category-1"},
					{TypeUrl: "type-2", Category: "category-2"},
				}},
			}, nil).Times(1)
			mockLinker.EXPECT().Disconnect(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
			doneEmit <- &DoneEmit{Err: io.EOF}

			Expect(serveDiscovery(edgeCtx, server, mockAds)).To(Succeed())

			nodes, err := storeMan.Nodes().ListNodes(context.Background(), instanceId)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes).To(HaveLen(1))
			Expect(nodes[0].GetAgentUuid()).To(Equal(edgeAgentUuid))
			Expect(nodes[0].GetName()).To(Equal("edge-1"))
			Expect(nodes[0].GetNode().GetClientType()).To(Equal("starling"))
			Expect(nodes[0].GetNode().GetExtensions()).To(HaveLen(2))
		})
		It("should send the uuid of the declared agent of its certificate in a first empty response", func() {
			firstRequest()
			mockLinker.EXPECT().Disconnect(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
			doneEmit <- &DoneEmit{Err: io.EOF}

			callInfo, err := serveDiscoveryCall(edgeCtx, server, mockAds)

			Expect(err).ToNot(HaveOccurred())
			Expect(callInfo.ResponseHeader().Get(helpers.HeaderAgentUuid)).To(Equal(edgeAgentUuid))
			Expect(sent).ToNot(BeEmpty())
			Expect(sent[0].GetResources()).To(BeEmpty())
			Expect(sent[0].GetRemovedResources()).To(BeEmpty())
			Expect(sent[0].GetResponseNonce()).To(BeEmpty())
			nodes, err := storeMan.Nodes().ListNodes(context.Background(), instanceId)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodes).To(HaveLen(1))
			Expect(nodes[0].GetAgentUuid()).To(Equal(edgeAgentUuid))
			Expect(nodes[0].GetName()).To(Equal("edge-1"))
		})
		It("should refuse a certificate naming no declared agent", func() {
			firstRequest()

			err := serveDiscovery(agentContext("ghost"), server, mockAds)

			expectCode(err, connect.CodePermissionDenied)
			Expect(err.Error()).To(ContainSubstring("ghost"))
			Expect(err.Error()).To(ContainSubstring(instanceId))
			Expect(sent).To(BeEmpty())
		})
		It("should refuse a call with no client certificate", func() {
			firstRequest()

			err := serveDiscovery(context.Background(), server, mockAds)

			expectCode(err, connect.CodePermissionDenied)
			Expect(err.Error()).To(ContainSubstring("client certificate is required"))
		})
		It("should refuse an agent of an instance with no instantiator record", func() {
			multi, err := instantiaters.NewDbMulti(context.Background(), dbMan, nil)
			Expect(err).ToNot(HaveOccurred())
			server = NewFlockServer(context.Background(), nil, config.AdminConfig{}, mockLinker, storeMan, nil, nil, nil, multi, nil, lockers.NewMemory(), nil)
			configure(server)
			firstRequest()

			err = serveDiscovery(edgeCtx, server, mockAds)

			expectCode(err, connect.CodePermissionDenied)
		})
		When("a node of the agent was seen from another address", func() {
			BeforeEach(func() {
				err := storeMan.Nodes().ClaimNode(context.Background(), instanceId, &svclookup.InstanceNode{
					AgentUuid: edgeAgentUuid,
					Name:      "edge-1",
				}, "10.0.0.9", 90*time.Second)
				Expect(err).ToNot(HaveOccurred())
			})
			It("should refuse the stream", func() {
				firstRequest()

				err := serveDiscovery(edgeCtx, server, mockAds)

				expectCode(err, connect.CodeFailedPrecondition)
				Expect(err.Error()).To(ContainSubstring("10.0.0.9"))
				Expect(err.Error()).To(ContainSubstring("127.0.0.1"))
				Expect(sent).To(BeEmpty())
			})
		})
		It("should accept a stream from the address of a fresh node", func() {
			err := storeMan.Nodes().ClaimNode(context.Background(), instanceId, &svclookup.InstanceNode{
				AgentUuid: edgeAgentUuid,
				Name:      "edge-1",
			}, "127.0.0.1", 90*time.Second)
			Expect(err).ToNot(HaveOccurred())
			firstRequest()
			mockLinker.EXPECT().Disconnect(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
			doneEmit <- &DoneEmit{Err: io.EOF}

			Expect(serveDiscovery(edgeCtx, server, mockAds)).To(Succeed())
		})
		It("should refuse a stream when another controller sharing the store holds the agent from another address", func() {
			Expect(dbStoreMan.Instances().Create(context.Background(), &instance.Instance{UUID: instanceId, Name: instanceId})).To(Succeed())
			other := NewFlockServer(context.Background(), nil, config.AdminConfig{}, mockLinker, dbStoreMan, nil, nil, nil, edgeInstantiater(), nil, lockers.NewMemory(), nil)
			configure(other)
			server = NewFlockServer(context.Background(), nil, config.AdminConfig{}, mockLinker, dbStoreMan, nil, nil, nil, edgeInstantiater(), nil, lockers.NewMemory(), nil)
			configure(server)
			firstRequest()
			mockLinker.EXPECT().Disconnect(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
			doneEmit <- &DoneEmit{Err: io.EOF}
			Expect(serveDiscovery(withPeerAddr(edgeCtx, "10.0.0.9:41000"), other, mockAds)).To(Succeed())
			firstRequest()

			err := serveDiscovery(edgeCtx, server, mockAds)

			expectCode(err, connect.CodeFailedPrecondition)
			Expect(err.Error()).To(ContainSubstring("10.0.0.9"))
			Expect(err.Error()).To(ContainSubstring("127.0.0.1"))
		})
		When("invalid parameter in recv", func() {
			It("should return error", func() {
				mockAds.EXPECT().Receive().DoAndReturn(func() (*svcdiscovery.DeltaDiscoveryRequest, error) {
					return &svcdiscovery.DeltaDiscoveryRequest{
						TypeUrl: "type-url",
					}, nil
				}).Times(1)

				err := serveDiscovery(edgeCtx, server, mockAds)
				Expect(err).To(HaveOccurred())
				Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
			})
		})
		When("agent is not initiated with mtls", func() {
			BeforeEach(func() {
				auth.SetTestMode(false)
			})
			AfterEach(func() {
				auth.SetTestMode(true)
			})
			It("should return error", func() {
				// EOF is the error returned by the server when the client closes the connection and is not considered as an error by server
				mockListen.EXPECT().OnAnnounce(gomock.Any(), gomock.Any()).DoAndReturn(func(eventName announcers.EventName, resources []*svcdiscovery.Resource) {
					doneEmit <- &DoneEmit{Err: io.EOF}
				}).Times(1)
				done := make(chan struct{})

				go func() {
					defer GinkgoRecover()
					err := serveDiscovery(context.Background(), server, mockAds)
					Expect(err).To(HaveOccurred())
					close(done)
				}()
				mockListen.OnAnnounce(announcers.ChangeEvent, []*svcdiscovery.Resource{})
				Eventually(done).Should(BeClosed())
			})
		})
		When("listener give a done error", func() {
			It("should return error and stop", func() {
				mockListen.EXPECT().OnAnnounce(gomock.Any(), gomock.Any()).DoAndReturn(func(eventName announcers.EventName, resources []*svcdiscovery.Resource) {
					doneEmit <- &DoneEmit{Err: fmt.Errorf("error")}
				}).Times(1)
				mockAds.EXPECT().Receive().DoAndReturn(func() (*svcdiscovery.DeltaDiscoveryRequest, error) {
					return &svcdiscovery.DeltaDiscoveryRequest{
						TypeUrl:      "type-url",
						InstanceUuid: instanceId,
					}, nil
				}).Times(1)
				mockLinker.EXPECT().Disconnect(gomock.Any(), gomock.Any(), gomock.Any()).Times(1)
				done := make(chan struct{})

				go func() {
					defer GinkgoRecover()
					err := serveDiscovery(edgeCtx, server, mockAds)
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(Equal("error"))
					close(done)
				}()
				mockListen.OnAnnounce(announcers.ChangeEvent, []*svcdiscovery.Resource{})
				Eventually(done).Should(BeClosed())
			})
		})
	})
})
