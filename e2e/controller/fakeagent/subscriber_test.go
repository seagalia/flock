//go:build e2e

package fakeagent_test

import (
	"context"
	"io"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/e2e/controller/fakeagent"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/flock/services/discovery/v1/svcdiscoveryconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/observe"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
)

const instanceUuid = "instance-1"

var vipType = helpers.TypeUrl[*vip.VIP]()

var _ = Describe("Subscriber", func() {
	var controller *inProcessController
	var subscriber *fakeagent.Subscriber
	var ctx context.Context

	updateVip := func(uuid, ip string) string {
		updated := &vip.VIP{UUID: uuid, Name: uuid, Ip: ip}
		Expect(controller.storeMan.VIPs().Update(context.Background(), uuid, updated)).To(Succeed())
		sig, err := helpers.MessageSignature(updated)
		Expect(err).NotTo(HaveOccurred())
		<-observe.EmitSubject(observer.EventType_UPDATE_SUBJECT, &observer.Subject{
			TypeUrl: vipType, Identifier: uuid, Signature: sig,
		})
		return sig
	}

	signatureOf := func(uuid string) func() string {
		return func() string {
			return subscriber.State(vipType)[uuid].GetSignature()
		}
	}

	BeforeEach(func() {
		controller = startController()
		Expect(controller.storeMan.VIPs().Create(context.Background(), &vip.VIP{UUID: "vip-1", Name: "vip-1", Ip: "10.0.0.1"})).To(Succeed())
		Expect(controller.storeMan.VIPs().Create(context.Background(), &vip.VIP{UUID: "vip-2", Name: "vip-2", Ip: "10.0.0.2"})).To(Succeed())
		Expect(controller.storeMan.Instances().Create(context.Background(), &instance.Instance{
			UUID: instanceUuid, Name: instanceUuid, VipsUuid: []string{"vip-1", "vip-2"},
		})).To(Succeed())

		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(context.Background())
		subscriber = fakeagent.New(instanceUuid, certs.SubscriberTLSConfig("localhost"))
		DeferCleanup(func() {
			subscriber.Drop()
			cancel()
		})
	})

	It("receives the state of its instance on connect", func() {
		Expect(subscriber.Connect(ctx, controller.addr, vipType)).To(Succeed())

		Eventually(func() map[string]*svcdiscovery.Resource { return subscriber.State(vipType) }).
			Should(SatisfyAll(HaveKey("vip-1"), HaveKey("vip-2")))
		Expect(subscriber.Err()).NotTo(HaveOccurred())
	})

	It("acks every response so live changes keep arriving", func() {
		Expect(subscriber.Connect(ctx, controller.addr, vipType)).To(Succeed())
		Eventually(func() map[string]*svcdiscovery.Resource { return subscriber.State(vipType) }).Should(HaveKey("vip-1"))

		firstSig := updateVip("vip-1", "10.0.1.1")
		Eventually(signatureOf("vip-1")).Should(Equal(firstSig))

		secondSig := updateVip("vip-1", "10.0.2.1")
		Eventually(signatureOf("vip-1")).Should(Equal(secondSig))
		Expect(subscriber.Err()).NotTo(HaveOccurred())
	})

	It("sends its held versions when reconnecting", func() {
		Expect(subscriber.Connect(ctx, controller.addr, vipType)).To(Succeed())
		Eventually(func() map[string]*svcdiscovery.Resource { return subscriber.State(vipType) }).
			Should(SatisfyAll(HaveKey("vip-1"), HaveKey("vip-2")))

		subscriber.Drop()
		subscriber.ClearHistory()
		updatedSig := updateVip("vip-2", "10.0.1.2")
		Expect(subscriber.Connect(ctx, controller.addr, vipType)).To(Succeed())

		Eventually(func() []*svcdiscovery.DeltaDiscoveryResponse { return subscriber.History(vipType) }).Should(HaveLen(1))
		first := subscriber.History(vipType)[0]
		Expect(first.GetResources()).To(HaveLen(1))
		Expect(first.GetResources()[0].GetUuid()).To(Equal("vip-2"))
		Expect(first.GetResources()[0].GetSignature()).To(Equal(updatedSig))
		Expect(first.GetRemovedResources()).To(BeEmpty())
	})

	It("gets its state on an immediate reconnect after a drop", func() {
		Expect(subscriber.Connect(ctx, controller.addr, fakeagent.SubscriberTypeUrls...)).To(Succeed())
		subscriber.Drop()
		// without the held state, only the second connection can bring the vips back
		subscriber.ForgetState()

		reconnectStart := time.Now()
		Expect(subscriber.Connect(ctx, controller.addr, fakeagent.SubscriberTypeUrls...)).To(Succeed())
		Expect(time.Since(reconnectStart)).To(BeNumerically("<", time.Second))

		Eventually(func() map[string]*svcdiscovery.Resource { return subscriber.State(vipType) }).
			Should(SatisfyAll(HaveKey("vip-1"), HaveKey("vip-2")))
		Expect(subscriber.Err()).NotTo(HaveOccurred())
	})

	It("applies removals", func() {
		Expect(subscriber.Connect(ctx, controller.addr, vipType)).To(Succeed())
		Eventually(func() map[string]*svcdiscovery.Resource { return subscriber.State(vipType) }).Should(HaveKey("vip-1"))

		Expect(controller.storeMan.VIPs().Delete(context.Background(), "vip-1")).To(Succeed())
		<-observe.EmitSubject(observer.EventType_DELETE_SUBJECT, &observer.Subject{
			TypeUrl: vipType, Identifier: "vip-1",
		})

		Eventually(func() map[string]*svcdiscovery.Resource { return subscriber.State(vipType) }).ShouldNot(HaveKey("vip-1"))
		Expect(subscriber.State(vipType)).To(HaveKey("vip-2"))
	})

	It("drops like a crash, without half-closing its streams", func() {
		recorder := &recvRecorder{secondRecvErrs: make(chan error, len(fakeagent.SubscriberTypeUrls))}
		recorderAddr := startRecorder(recorder)
		Expect(subscriber.Connect(ctx, recorderAddr, fakeagent.SubscriberTypeUrls...)).To(Succeed())
		Eventually(recorder.waitingCount).Should(Equal(len(fakeagent.SubscriberTypeUrls)))

		subscriber.Drop()

		for range fakeagent.SubscriberTypeUrls {
			var recvErr error
			Eventually(recorder.secondRecvErrs).Should(Receive(&recvErr))
			Expect(recvErr).To(HaveOccurred())
			// a half-close ends the stream with io.EOF itself, an abort with an RPC error wrapping it
			Expect(recvErr).NotTo(BeIdenticalTo(io.EOF))
		}
		Expect(subscriber.Err()).NotTo(HaveOccurred())
	})

	It("records a refusal on every type url without failing to connect", func() {
		stranger := fakeagent.New("unknown-instance", certs.SubscriberTLSConfig("localhost"))
		DeferCleanup(stranger.Drop)

		Expect(stranger.Connect(ctx, controller.addr, fakeagent.SubscriberTypeUrls...)).To(Succeed())

		Eventually(func() connect.Code { return connect.CodeOf(stranger.Err()) }).Should(Equal(connect.CodeNotFound))
	})

	It("records a refusal for an unknown instance", func() {
		stranger := fakeagent.New("unknown-instance", certs.SubscriberTLSConfig("localhost"))
		DeferCleanup(stranger.Drop)

		Expect(stranger.Connect(ctx, controller.addr, vipType)).To(Succeed())

		Eventually(func() connect.Code { return connect.CodeOf(stranger.Err()) }).Should(Equal(connect.CodeNotFound))
	})
})

// recvRecorder is a discovery server that never answers: after the first request its Recv waits
// for whatever the agent does with the stream, and reports it
type recvRecorder struct {
	mutex          sync.Mutex
	waiting        int
	secondRecvErrs chan error
}

func (r *recvRecorder) DeltaAggregatedResources(_ context.Context, stream svcdiscoveryconnect.AggregatedDiscoveryDeltaAggregatedResourcesServerStream) error {
	if _, err := stream.Receive(); err != nil {
		return err
	}
	r.mutex.Lock()
	r.waiting++
	r.mutex.Unlock()
	_, err := stream.Receive()
	r.secondRecvErrs <- err
	return nil
}

func (r *recvRecorder) waitingCount() int {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.waiting
}

func startRecorder(recorder *recvRecorder) string {
	connectServer := connect.NewServer()
	svcdiscoveryconnect.RegisterAggregatedDiscoveryHandler(connectServer, recorder)
	addr, stop := rpctest.Serve(connectServer, controllerServerTLSConfig())
	DeferCleanup(stop)
	return addr
}
