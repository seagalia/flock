package fakecontroller_test

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/agentsdk/aggregaters"
	"github.com/seagalia/flock/sdk/agentsdk/waiters"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/flock/services/discovery/v1/svcdiscoveryconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"

	. "github.com/seagalia/flock/sdk/agentsdk/fakecontroller"
)

var backendTypeUrl = helpers.TypeUrl[*backend.Backend]()

const agentName = "node-1"
const agentUuid = "agent-uuid-1"

// subscriber is an agent side stream driven by the real sdk aggregator
type subscriber struct {
	recv   chan *svcdiscovery.DeltaDiscoveryResponse
	ack    chan *svcdiscovery.DeltaDiscoveryRequest
	cancel context.CancelFunc
}

func subscribe(addr string, clientTLS *tls.Config, instance, typeUrl string, initVersions map[string]string) *subscriber {
	ctx, cancel := context.WithCancel(context.Background())
	sub := &subscriber{
		recv:   make(chan *svcdiscovery.DeltaDiscoveryResponse, 10),
		ack:    make(chan *svcdiscovery.DeltaDiscoveryRequest, 10),
		cancel: cancel,
	}
	aggr := aggregaters.NewAggregator(addr, instance, &core.Node{}, clientTLS, waiters.NewSuspendWaiter())
	go func() {
		defer GinkgoRecover()
		_ = aggr.DeltaAggregatedResources(ctx, typeUrl, nil, func() map[string]string { return initVersions }, sub.recv, sub.ack, nil)
	}()
	return sub
}

// next waits next response and acks it
func (s *subscriber) next() *svcdiscovery.DeltaDiscoveryResponse {
	GinkgoHelper()
	var resp *svcdiscovery.DeltaDiscoveryResponse
	Eventually(s.recv, 5*time.Second).Should(Receive(&resp))
	s.ack <- &svcdiscovery.DeltaDiscoveryRequest{TypeUrl: backendTypeUrl, ResponseNonce: resp.GetResponseNonce()}
	return resp
}

func uuids(resp *svcdiscovery.DeltaDiscoveryResponse) []string {
	result := make([]string, 0, len(resp.GetResources()))
	for _, res := range resp.GetResources() {
		result = append(result, res.GetUuid())
	}
	return result
}

var _ = Describe("Fake controller", func() {
	var (
		server    *Server
		addr      string
		serverTLS *tls.Config
		clientTLS *tls.Config
		subs      []*subscriber
	)

	start := func(instance, typeUrl string, initVersions map[string]string) *subscriber {
		sub := subscribe(addr, clientTLS, instance, typeUrl, initVersions)
		subs = append(subs, sub)
		return sub
	}

	BeforeEach(func() {
		serverTLS, clientTLS = rpctest.NewTLS(agentName)
		server = New(serverTLS)
		server.DeclareAgent(agentName, agentUuid)
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		addr = lis.Addr().String()
		go func() {
			_ = server.Serve(lis)
		}()
		subs = nil
	})

	AfterEach(func() {
		// let in flight acks be sent before cancelling: the sdk aggregator closes its stream while
		// its sender goroutine may still be sending (race inside the sdk, not in the fake controller)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		for _, instance := range []string{"inst-1", "inst-2"} {
			_ = server.WaitAcked(ctx, instance)
		}
		for _, sub := range subs {
			sub.cancel()
		}
		server.Stop()
	})

	It("sends current state on subscribe", func() {
		server.Push("inst-1", &backend.Backend{UUID: "backend-a", Name: "a"})
		sub := start("inst-1", backendTypeUrl, nil)

		resp := sub.next()
		Expect(uuids(resp)).To(Equal([]string{"backend-a"}))
		Expect(resp.GetResources()[0].GetSignature()).ToNot(BeEmpty())
		received := &backend.Backend{}
		Expect(resp.GetResources()[0].GetResource().UnmarshalTo(received)).To(Succeed())
		Expect(received.GetName()).To(Equal("a"))
	})

	It("streams pushes and removals after subscribe", func() {
		sub := start("inst-1", backendTypeUrl, nil)
		Expect(sub.next().GetResources()).To(BeEmpty(), "initial state is empty")

		server.Push("inst-1", &backend.Backend{UUID: "backend-b"})
		Expect(uuids(sub.next())).To(Equal([]string{"backend-b"}))

		server.Remove("inst-1", backendTypeUrl, "backend-b")
		Expect(sub.next().GetRemovedResources()).To(Equal([]string{"backend-b"}))
	})

	It("only sends resources of the subscribed type", func() {
		sub := start("inst-1", backendTypeUrl, nil)
		sub.next()
		server.Push("inst-1", &vip.VIP{UUID: "vip-1", Ip: "10.0.0.1"})
		Consistently(sub.recv, 300*time.Millisecond).ShouldNot(Receive())
	})

	It("resyncs a reconnecting client from its initial versions", func() {
		backendA := &backend.Backend{UUID: "backend-a"}
		server.Push("inst-1", backendA)
		sig, err := helpers.MessageSignature(backendA)
		Expect(err).ToNot(HaveOccurred())

		sub := start("inst-1", backendTypeUrl, map[string]string{"backend-a": sig, "gone": "x"})
		resp := sub.next()
		Expect(uuids(resp)).To(BeEmpty(), "unchanged resource must not be resent")
		Expect(resp.GetRemovedResources()).To(Equal([]string{"gone"}))
	})

	It("isolates resources per instance uuid", func() {
		sub := start("inst-1", backendTypeUrl, nil)
		sub.next()
		server.Push("inst-2", &backend.Backend{UUID: "backend-other"})
		Consistently(sub.recv, 300*time.Millisecond).ShouldNot(Receive())
	})

	It("waits acks", func() {
		sub := start("inst-1", backendTypeUrl, nil)
		sub.next()
		Expect(server.WaitAcked(context.Background(), "inst-1")).To(Succeed())

		server.Push("inst-1", &backend.Backend{UUID: "backend-c"})
		var resp *svcdiscovery.DeltaDiscoveryResponse
		Eventually(sub.recv, 5*time.Second).Should(Receive(&resp))

		acked := make(chan error, 1)
		go func() {
			acked <- server.WaitAcked(context.Background(), "inst-1")
		}()
		Consistently(acked, 200*time.Millisecond).ShouldNot(Receive())

		sub.ack <- &svcdiscovery.DeltaDiscoveryRequest{TypeUrl: backendTypeUrl, ResponseNonce: resp.GetResponseNonce()}
		Eventually(acked, 5*time.Second).Should(Receive(BeNil()))

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		server.Push("inst-1", &backend.Backend{UUID: "backend-d"})
		Eventually(sub.recv, 5*time.Second).Should(Receive())
		Expect(server.WaitAcked(ctx, "inst-1")).To(MatchError(context.DeadlineExceeded))
	})

	It("coalesces changes made while a response is not acked", func() {
		sub := start("inst-1", backendTypeUrl, nil)
		sub.next()
		server.Push("inst-1", &backend.Backend{UUID: "backend-1"})
		var first *svcdiscovery.DeltaDiscoveryResponse
		Eventually(sub.recv, 5*time.Second).Should(Receive(&first))

		server.Push("inst-1", &backend.Backend{UUID: "backend-2"})
		server.Push("inst-1", &backend.Backend{UUID: "backend-3"})
		Consistently(sub.recv, 200*time.Millisecond).ShouldNot(Receive(), "nothing is sent before ack")

		sub.ack <- &svcdiscovery.DeltaDiscoveryRequest{TypeUrl: backendTypeUrl, ResponseNonce: first.GetResponseNonce()}
		Expect(uuids(sub.next())).To(ConsistOf("backend-2", "backend-3"))
	})

	It("removes every resources of an instance", func() {
		server.Push("inst-1", &backend.Backend{UUID: "backend-a"}, &backend.Backend{UUID: "backend-b"})
		sub := start("inst-1", backendTypeUrl, nil)
		sub.next()
		server.RemoveAll("inst-1")
		Expect(sub.next().GetRemovedResources()).To(ConsistOf("backend-a", "backend-b"))
	})

	It("lets a client reconnect after its streams are dropped and sends what changed meanwhile", func() {
		server.Push("inst-1", &backend.Backend{UUID: "backend-a"})
		sub := start("inst-1", backendTypeUrl, nil)
		Expect(uuids(sub.next())).To(Equal([]string{"backend-a"}))

		server.DropStreams("inst-1")
		Eventually(func() bool { return server.Subscribed("inst-1", backendTypeUrl) }).Should(BeFalse())

		// the sdk client reconnects by itself, here a new client reconnects with what it knew
		sig, err := helpers.MessageSignature(&backend.Backend{UUID: "backend-a"})
		Expect(err).ToNot(HaveOccurred())
		server.Remove("inst-1", backendTypeUrl, "backend-a")
		resub := start("inst-1", backendTypeUrl, map[string]string{"backend-a": sig})
		Expect(resub.next().GetRemovedResources()).To(Equal([]string{"backend-a"}))
	})

	It("refuses streams of an instance during an outage", func() {
		sub := start("inst-1", backendTypeUrl, nil)
		sub.next()

		server.StartOutage("inst-1")
		Eventually(func() bool { return server.Subscribed("inst-1", backendTypeUrl) }).Should(BeFalse())
		server.Push("inst-1", &backend.Backend{UUID: "backend-during-outage"})
		start("inst-1", backendTypeUrl, nil)
		Eventually(func() []time.Time { return server.Refusals("inst-1", backendTypeUrl) }).Should(HaveLen(1))
		Consistently(func() bool { return server.Subscribed("inst-1", backendTypeUrl) }, 300*time.Millisecond).Should(BeFalse())
		Expect(server.Refusals("inst-2", backendTypeUrl)).To(BeEmpty())

		server.EndOutage("inst-1")
		Expect(uuids(start("inst-1", backendTypeUrl, nil).next())).To(Equal([]string{"backend-during-outage"}))
	})

	It("reports subscribed type urls", func() {
		vipTypeUrl := helpers.TypeUrl[*vip.VIP]()
		Expect(server.Subscribed("inst-1", backendTypeUrl)).To(BeFalse())
		start("inst-1", backendTypeUrl, nil).next()
		Eventually(func() bool { return server.Subscribed("inst-1", backendTypeUrl) }).Should(BeTrue())
		Expect(server.Subscribed("inst-1", backendTypeUrl, vipTypeUrl)).To(BeFalse())
		Expect(server.Subscribed("inst-2", backendTypeUrl)).To(BeFalse())
	})

	Context("identity", func() {
		// rawStream opens a discovery stream with the connect client, so the identity response is not hidden by the sdk
		// aggregator
		rawStream := func(ctx context.Context) (*svcdiscoveryconnect.AggregatedDiscoveryDeltaAggregatedResourcesClientStream, *connect.CallInfo) {
			clients := helpers.NewGrpcClients(clientTLS)
			DeferCleanup(clients.Close)
			ctx, callInfo := connect.NewClientContext(ctx)
			stream, err := svcdiscoveryconnect.NewAggregatedDiscoveryClient(clients.Client(addr)).DeltaAggregatedResources(ctx)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(stream.Close)
			Expect(stream.Send(&svcdiscovery.DeltaDiscoveryRequest{TypeUrl: backendTypeUrl, InstanceUuid: "inst-1"})).To(Succeed())
			return &stream, callInfo
		}

		It("answers the declared uuid in a header with a first empty response", func(ctx SpecContext) {
			server.Push("inst-1", &backend.Backend{UUID: "backend-a"})
			stream, callInfo := rawStream(ctx)

			first, err := stream.Receive()
			Expect(err).ToNot(HaveOccurred())
			Expect(callInfo.ResponseHeader().Get(helpers.HeaderAgentUuid)).To(Equal(agentUuid))
			Expect(first.GetResources()).To(BeEmpty())
			Expect(first.GetRemovedResources()).To(BeEmpty())
			Expect(first.GetResponseNonce()).To(BeEmpty())

			second, err := stream.Receive()
			Expect(err).ToNot(HaveOccurred())
			Expect(uuids(second)).To(Equal([]string{"backend-a"}))
			Expect(second.GetResponseNonce()).ToNot(BeEmpty())
		}, SpecTimeout(5*time.Second))

		It("refuses a certificate naming no declared agent", func(ctx SpecContext) {
			undeclaring := New(serverTLS)
			undeclaring.DeclareAgent("other", "agent-uuid-2")
			lis, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			go func() {
				_ = undeclaring.Serve(lis)
			}()
			DeferCleanup(undeclaring.Stop)
			addr = lis.Addr().String()
			stream, _ := rawStream(ctx)

			_, err = stream.Receive()
			Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied))
			Expect(err).To(MatchError(ContainSubstring(agentName)))
		}, SpecTimeout(5*time.Second))
	})
})
