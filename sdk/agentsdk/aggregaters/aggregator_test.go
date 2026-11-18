package aggregaters_test

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gbytes"

	. "github.com/seagalia/flock/sdk/agentsdk/aggregaters"
	"github.com/seagalia/flock/sdk/agentsdk/fakecontroller"
	"github.com/seagalia/flock/sdk/agentsdk/waiters"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
)

const typeUrl = "type.googleapis.com/test.Resource"

var _ = Describe("Aggregator", func() {
	var (
		receiveResp  chan *svcdiscovery.DeltaDiscoveryResponse
		responseChan chan *svcdiscovery.DeltaDiscoveryRequest
		node         = &core.Node{}
		noInitial    = func() map[string]string { return nil }
	)

	BeforeEach(func() {
		receiveResp = make(chan *svcdiscovery.DeltaDiscoveryResponse, 10)
		responseChan = make(chan *svcdiscovery.DeltaDiscoveryRequest, 10)
	})

	newAggregator := func(handle func(ctx context.Context, stream adsStream) error) *Aggregator {
		server, clientTLS := startFakeAdsServer(handle)
		aggr := NewAggregator(server.addr, "instance-1", node, clientTLS, waiters.NewSuspendWaiter())
		DeferCleanup(aggr.Close)
		return aggr
	}

	aggregate := func(ctx context.Context, aggr *Aggregator, startHook func()) chan error {
		result := make(chan error, 1)
		received, requests := receiveResp, responseChan
		go func() {
			result <- aggr.DeltaAggregatedResources(ctx, typeUrl, nil, noInitial, received, requests, startHook)
		}()
		return result
	}

	It("should send a first request describing the agent and its known resources", func() {
		firstRequests := make(chan *svcdiscovery.DeltaDiscoveryRequest, 1)
		aggr := newAggregator(func(ctx context.Context, stream adsStream) error {
			req, err := stream.Receive()
			if err != nil {
				return err
			}
			firstRequests <- req
			<-ctx.Done()
			return nil
		})

		filters := svccore.ListFilters{{Key: "team", Value: "web", IsMetadata: true}}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			_ = aggr.DeltaAggregatedResources(ctx, typeUrl, filters, func() map[string]string {
				return map[string]string{"res-1": "sig-1"}
			}, receiveResp, responseChan, nil)
		}()

		var first *svcdiscovery.DeltaDiscoveryRequest
		Eventually(firstRequests, 5*time.Second).Should(Receive(&first))
		Expect(first.GetTypeUrl()).To(Equal(typeUrl))
		Expect(first.GetInstanceUuid()).To(Equal("instance-1"))
		Expect(first.GetInitialResourceVersions()).To(Equal(map[string]string{"res-1": "sig-1"}))
		Expect(first.GetFilters()).To(HaveLen(1))
		Expect(first.GetFilters()[0].GetKey()).To(Equal("team"))
	})

	It("should forward responses and send requests given back to the server", func() {
		acks := make(chan string, 1)
		aggr := newAggregator(func(ctx context.Context, stream adsStream) error {
			if _, err := stream.Receive(); err != nil {
				return err
			}
			err := stream.Send(&svcdiscovery.DeltaDiscoveryResponse{ResponseNonce: "nonce-1"})
			if err != nil {
				return err
			}
			ack, err := stream.Receive()
			if err != nil {
				return err
			}
			acks <- ack.GetResponseNonce()
			<-ctx.Done()
			return nil
		})

		var hookCalls atomic.Int64
		ctx, cancel := context.WithCancel(context.Background())
		result := aggregate(ctx, aggr, func() { hookCalls.Add(1) })

		var resp *svcdiscovery.DeltaDiscoveryResponse
		Eventually(receiveResp, 5*time.Second).Should(Receive(&resp))
		Expect(resp.GetResponseNonce()).To(Equal("nonce-1"))
		Expect(hookCalls.Load()).To(BeEquivalentTo(1))

		responseChan <- &svcdiscovery.DeltaDiscoveryRequest{TypeUrl: typeUrl, ResponseNonce: "nonce-1"}
		Eventually(acks, 5*time.Second).Should(Receive(Equal("nonce-1")))

		cancel()
		Eventually(result, 5*time.Second).Should(Receive(BeNil()))
	})

	It("should log the responses without their resources, which may be secrets", func() {
		logs := gbytes.NewBuffer()
		Expect(logging.Setup(logging.Options{Console: logs, Level: "debug", NoColor: true})).To(Succeed())
		DeferCleanup(func() {
			Expect(logging.Setup(logging.Options{Console: GinkgoWriter})).To(Succeed())
		})
		aSecret, err := svcdiscovery.NewResourceFromMessage(&secret.Secret{UUID: "s1", Data: map[string]string{"k": "very-secret"}})
		Expect(err).ToNot(HaveOccurred())
		aggr := newAggregator(func(ctx context.Context, stream adsStream) error {
			if _, err := stream.Receive(); err != nil {
				return err
			}
			err := stream.Send(&svcdiscovery.DeltaDiscoveryResponse{
				ResponseNonce: "nonce-1", Resources: []*svcdiscovery.Resource{aSecret},
			})
			if err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		aggregate(ctx, aggr, nil)

		Eventually(receiveResp, 5*time.Second).Should(Receive())
		Eventually(logs).Should(gbytes.Say("nonce-1"))
		Expect(string(logs.Contents())).To(ContainSubstring("s1"))
		Expect(string(logs.Contents())).ToNot(ContainSubstring("very-secret"))
	})

	It("should return the error ending the stream", func() {
		aggr := newAggregator(func(ctx context.Context, stream adsStream) error {
			return connect.NewError(connect.CodePermissionDenied, "denied")
		})

		result := aggregate(context.Background(), aggr, nil)
		var err error
		Eventually(result, 5*time.Second).Should(Receive(&err))
		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied))
	})

	It("should do nothing when context is already done", func() {
		aggr := NewAggregator("127.0.0.1:1", "instance-1", node, &tls.Config{}, waiters.NewSuspendWaiter())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(aggr.DeltaAggregatedResources(ctx, typeUrl, nil, noInitial, receiveResp, responseChan, nil)).To(Succeed())
	})

	It("should fail when it can't connect to the server", func() {
		aggr := NewAggregator("127.0.0.1:1", "instance-1", node, &tls.Config{}, waiters.NewSuspendWaiter())
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		Expect(aggr.DeltaAggregatedResources(ctx, typeUrl, nil, noInitial, receiveResp, responseChan, nil)).ToNot(Succeed())
	})

	It("should close at once while it connects to a server that never answers, as a normal end", func() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(listener.Close)
		aggr := NewAggregator(listener.Addr().String(), "instance-1", node, &tls.Config{}, waiters.NewSuspendWaiter())
		result := make(chan error, 1)
		go func() {
			result <- aggr.DeltaAggregatedResources(context.Background(), typeUrl, nil, noInitial, receiveResp, responseChan, nil)
		}()
		Consistently(result, 200*time.Millisecond).ShouldNot(Receive())

		closed := make(chan error, 1)
		go func() { closed <- aggr.Close() }()

		Eventually(closed, time.Second).Should(Receive(BeNil()))
		Eventually(result, time.Second).Should(Receive(BeNil()))
	})

	It("should end normally when the context ends while it connects", func() {
		// accepts the connection and never answers, so the reachability check runs until ctx ends
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(listener.Close)
		aggr := NewAggregator(listener.Addr().String(), "instance-1", node, &tls.Config{}, waiters.NewSuspendWaiter())
		DeferCleanup(aggr.Close)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		Expect(aggr.DeltaAggregatedResources(ctx, typeUrl, nil, noInitial, receiveResp, responseChan, nil)).To(Succeed())
	})

	It("should end normally a stream cut by the server on a closed aggregator, then refuse to connect", func() {
		serverDone := make(chan struct{})
		aggr := newAggregator(func(ctx context.Context, stream adsStream) error {
			if _, err := stream.Receive(); err != nil {
				return err
			}
			err := stream.Send(&svcdiscovery.DeltaDiscoveryResponse{ResponseNonce: "nonce-1"})
			close(serverDone)
			// returning ends the stream: the agent receives io.EOF
			return err
		})
		// unbuffered and not read: the aggregator stays blocked on forwarding the response
		receiveResp = make(chan *svcdiscovery.DeltaDiscoveryResponse)
		result := aggregate(context.Background(), aggr, nil)

		Eventually(serverDone, 5*time.Second).Should(BeClosed())
		// closing before io.EOF reaches the agent would end the stream with another error
		time.Sleep(200 * time.Millisecond)
		Expect(aggr.Close()).To(Succeed())
		Eventually(receiveResp, 5*time.Second).Should(Receive())
		Eventually(result, 5*time.Second).Should(Receive(BeNil()), "io.EOF on a closed aggregator is a normal end")

		Expect(aggr.Connect(context.Background())).To(MatchError(ErrClosed))
	})

	It("should refuse to stream on a closed aggregator, as a normal end", func() {
		aggr := newAggregator(func(ctx context.Context, stream adsStream) error { return nil })
		Expect(aggr.Close()).To(Succeed())

		Expect(aggr.DeltaAggregatedResources(context.Background(), typeUrl, nil, noInitial, receiveResp, responseChan, nil)).
			To(Succeed())
	})

	It("should return when the context ends while the receive is suspended", func() {
		server, clientTLS := startFakeAdsServer(func(ctx context.Context, stream adsStream) error {
			if _, err := stream.Receive(); err != nil {
				return err
			}
			if err := stream.Send(&svcdiscovery.DeltaDiscoveryResponse{ResponseNonce: "nonce-1"}); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		})
		waiter := waiters.NewSuspendWaiter()
		waiter.Suspend()
		aggr := NewAggregator(server.addr, "instance-1", node, clientTLS, waiter)
		DeferCleanup(aggr.Close)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := aggregate(ctx, aggr, nil)
		Consistently(result, 300*time.Millisecond).ShouldNot(Receive())

		cancel()
		Eventually(result, 2*time.Second).Should(Receive(BeNil()))
	})

	It("should abort its streams on Close, as a crash would, without half-closing them", func() {
		secondReceiveErrs := make(chan error, 1)
		aggr := newAggregator(func(ctx context.Context, stream adsStream) error {
			if _, err := stream.Receive(); err != nil {
				return err
			}
			_, err := stream.Receive()
			secondReceiveErrs <- err
			return nil
		})
		result := aggregate(context.Background(), aggr, nil)
		Consistently(result, 200*time.Millisecond).ShouldNot(Receive())

		Expect(aggr.Close()).To(Succeed())

		var receiveErr error
		Eventually(secondReceiveErrs, 5*time.Second).Should(Receive(&receiveErr))
		Expect(receiveErr).To(HaveOccurred())
		Expect(receiveErr).NotTo(BeIdenticalTo(io.EOF), "a half-close ends the stream with io.EOF itself")
		Eventually(result, 5*time.Second).Should(Receive(BeNil()))
	})

	It("should stop cleanly when context is cancelled while requests are sent", func() {
		// meant for -race: teardown must not race with Send and Recv
		aggr := newAggregator(func(ctx context.Context, stream adsStream) error {
			go func() {
				for {
					if _, err := stream.Receive(); err != nil {
						return
					}
				}
			}()
			for i := 0; ; i++ {
				err := stream.Send(&svcdiscovery.DeltaDiscoveryResponse{ResponseNonce: strconv.Itoa(i)})
				if err != nil {
					return err
				}
				time.Sleep(time.Millisecond)
			}
		})

		for range 20 {
			ctx, cancel := context.WithCancel(context.Background())
			result := aggregate(ctx, aggr, func() {})
			stopAcking := make(chan struct{})
			received, requests := receiveResp, responseChan
			go func() {
				for {
					select {
					case <-stopAcking:
						return
					case resp := <-received:
						requests <- &svcdiscovery.DeltaDiscoveryRequest{ResponseNonce: resp.GetResponseNonce()}
					}
				}
			}()
			time.Sleep(20 * time.Millisecond)
			cancel()
			Eventually(result, 5*time.Second).Should(Receive(BeNil()))
			close(stopAcking)
		}
	})

	It("should run the start hook on the first forwarded response, after the identity response", func() {
		sendResource := make(chan struct{})
		aggr := newAggregator(func(ctx context.Context, stream adsStream) error {
			if _, err := stream.Receive(); err != nil {
				return err
			}
			if err := stream.Send(&svcdiscovery.DeltaDiscoveryResponse{}); err != nil {
				return err
			}
			<-sendResource
			if err := stream.Send(&svcdiscovery.DeltaDiscoveryResponse{ResponseNonce: "nonce-1"}); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		})

		var hookCalls atomic.Int64
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		aggregate(ctx, aggr, func() { hookCalls.Add(1) })

		Consistently(receiveResp, 300*time.Millisecond).ShouldNot(Receive())
		Expect(hookCalls.Load()).To(BeZero())
		close(sendResource)
		var resp *svcdiscovery.DeltaDiscoveryResponse
		Eventually(receiveResp, 5*time.Second).Should(Receive(&resp))
		Expect(resp.GetResponseNonce()).To(Equal("nonce-1"))
		Expect(hookCalls.Load()).To(BeEquivalentTo(1))
	})

	It("should forward an empty response with no nonce coming after the first one", func() {
		aggr := newAggregator(func(ctx context.Context, stream adsStream) error {
			if _, err := stream.Receive(); err != nil {
				return err
			}
			if err := stream.Send(&svcdiscovery.DeltaDiscoveryResponse{}); err != nil {
				return err
			}
			if err := stream.Send(&svcdiscovery.DeltaDiscoveryResponse{}); err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		aggregate(ctx, aggr, nil)

		Eventually(receiveResp, 5*time.Second).Should(Receive())
		Consistently(receiveResp, 300*time.Millisecond).ShouldNot(Receive())
	})

	Context("against the fake controller", func() {
		const agentUuid = "agent-uuid-1"
		var (
			controller *fakecontroller.Server
			aggr       *Aggregator
		)

		BeforeEach(func() {
			serverTLS, clientTLS := rpctest.NewTLS("node-1")
			controller = fakecontroller.New(serverTLS)
			controller.DeclareAgent("node-1", agentUuid)
			lis, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			go func() {
				_ = controller.Serve(lis)
			}()
			DeferCleanup(controller.Stop)
			aggr = NewAggregator(lis.Addr().String(), "instance-1", node, clientTLS, waiters.NewSuspendWaiter())
			DeferCleanup(aggr.Close)
		})

		// next waits the next forwarded response and acks it
		next := func() *svcdiscovery.DeltaDiscoveryResponse {
			GinkgoHelper()
			var resp *svcdiscovery.DeltaDiscoveryResponse
			Eventually(receiveResp, 5*time.Second).Should(Receive(&resp))
			responseChan <- &svcdiscovery.DeltaDiscoveryRequest{TypeUrl: typeUrl, ResponseNonce: resp.GetResponseNonce()}
			return resp
		}

		It("should give the agent uuid while no resource exists, without forwarding the identity response", func(ctx SpecContext) {
			Expect(aggr.AgentUuid()).To(BeEmpty())
			aggregate(ctx, aggr, nil)

			Expect(aggr.WaitAgentUuid(ctx)).To(Equal(agentUuid))
			Expect(aggr.AgentUuid()).To(Equal(agentUuid))
			first := next()
			Expect(first.GetResponseNonce()).ToNot(BeEmpty(), "the identity response has no nonce")
			Consistently(receiveResp, 300*time.Millisecond).ShouldNot(Receive())
		}, SpecTimeout(10*time.Second))

		It("should stop waiting the agent uuid when the context ends", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_, err := aggr.WaitAgentUuid(ctx)
			Expect(err).To(MatchError(context.DeadlineExceeded))
		})

		It("should forward and ack the resources sent after the identity response", func(ctx SpecContext) {
			go func() {
				secretTypeUrl := helpers.TypeUrl[*secret.Secret]()
				_ = aggr.DeltaAggregatedResources(ctx, secretTypeUrl, nil, noInitial, receiveResp, responseChan, nil)
			}()
			next()
			Expect(controller.WaitAcked(ctx, "instance-1")).To(Succeed())

			controller.Push("instance-1", &secret.Secret{UUID: "s1"})
			resp := next()
			Expect(resp.GetResources()).To(HaveLen(1))
			Expect(resp.GetResources()[0].GetUuid()).To(Equal("s1"))
			Expect(controller.WaitAcked(ctx, "instance-1")).To(Succeed())
		}, SpecTimeout(10*time.Second))

		It("should log an error and keep its first uuid when a connection answers another one", func(ctx SpecContext) {
			logs := gbytes.NewBuffer()
			Expect(logging.Setup(logging.Options{Console: logs, NoColor: true})).To(Succeed())
			DeferCleanup(func() {
				Expect(logging.Setup(logging.Options{Console: GinkgoWriter})).To(Succeed())
			})
			first := aggregate(ctx, aggr, nil)
			Expect(aggr.WaitAgentUuid(ctx)).To(Equal(agentUuid))

			controller.DeclareAgent("node-1", "agent-uuid-2")
			controller.DropStreams("instance-1")
			Eventually(first, 5*time.Second).Should(Receive(HaveOccurred()))
			aggregate(ctx, aggr, nil)

			Eventually(logs, 5*time.Second).Should(gbytes.Say("agent-uuid-2"))
			Expect(aggr.AgentUuid()).To(Equal(agentUuid))
		}, SpecTimeout(10*time.Second))
	})
})
