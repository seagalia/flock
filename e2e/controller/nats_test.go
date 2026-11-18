//go:build e2e

package e2e_test

import (
	"context"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"

	"github.com/seagalia/flock/e2e/controller/env"
)

// crossControllerTimeout covers a change going from controller A through nats to the announcer of controller B
const crossControllerTimeout = 60 * time.Second

// the admin talks to controller A and the agent to controller B, so every change reaches the agent only through nats
var _ = Describe("NATS", Ordered, func() {
	var (
		controllerA, controllerB *env.Controller
		admin                    *env.Api
		adminOnB                 *env.Api
	)

	BeforeAll(func(ctx SpecContext) {
		dsn, err := e2eEnv.StartPostgres(ctx)
		Expect(err).NotTo(HaveOccurred())
		natsURL, err := e2eEnv.StartNats(ctx)
		Expect(err).NotTo(HaveOccurred())
		controllerA = startController(ctx, env.ControllerOptions{
			Name: "controller-a", Store: env.StorePostgres, DSN: dsn, NatsURL: natsURL, Instantiators: env.SubscriberInstantiators(),
		})
		controllerB = startController(ctx, env.ControllerOptions{
			Name: "controller-b", Store: env.StorePostgres, DSN: dsn, NatsURL: natsURL, Instantiators: env.SubscriberInstantiators(),
		})
		admin = loginAdmin(ctx, controllerA)
		adminOnB = loginAdmin(ctx, controllerB)
	}, NodeTimeout(10*time.Minute))

	BeforeEach(func() {
		registerControllerForDump(controllerA)
		registerControllerForDump(controllerB)
	})

	It("pushes changes made on another controller", func(ctx SpecContext) {
		zone := subscriberZone(ctx, admin)
		firstVs, firstBackend := createTcpVs(ctx, admin, 8001)
		instanceUuid, firstVip := createInstance(ctx, admin, zone, firstVs)
		subscriber := connectSubscriber(ctx, controllerB, instanceUuid)
		Eventually(heldUuids(subscriber, vipType)).Should(ConsistOf(firstVip))
		Eventually(heldUuids(subscriber, backendType)).Should(ConsistOf(firstBackend))

		By("creating a virtual server attached to the vip")
		createdVs, createdBackend := createTcpVs(ctx, admin, 8002)
		setVipVirtualServers(ctx, admin, firstVip, firstVs, createdVs)
		Eventually(heldUuids(subscriber, vsType), crossControllerTimeout).Should(ConsistOf(firstVs, createdVs))
		Eventually(heldUuids(subscriber, backendType), crossControllerTimeout).Should(ConsistOf(firstBackend, createdBackend))

		By("updating a backend")
		updateBackendPort(ctx, admin, firstBackend, 9090)
		Eventually(heldBackendPort(subscriber, firstBackend), crossControllerTimeout).Should(BeEquivalentTo(9090))

		By("detaching the created virtual server")
		setVipVirtualServers(ctx, admin, firstVip, firstVs)
		Eventually(heldUuids(subscriber, vsType), crossControllerTimeout).Should(ConsistOf(firstVs))
		Eventually(heldUuids(subscriber, backendType), crossControllerTimeout).Should(ConsistOf(firstBackend))

		By("deleting the detached virtual server")
		subscriber.ClearHistory()
		deleteVsOnAExpectGoneOnB(ctx, admin, adminOnB, createdVs)
		// controller refuses to delete an attached virtual server, so an agent never sees a delete on its own
		Consistently(receivedUuids(subscriber), nothingElseArrives).Should(BeEmpty())
		Expect(subscriber.Err()).NotTo(HaveOccurred())
	}, NodeTimeout(5*time.Minute))

	It("sends the delta when reconnecting to the other controller", func(ctx SpecContext) {
		zone := subscriberZone(ctx, admin)
		keptVs, updatedBackend := createTcpVs(ctx, admin, 8001)
		deletedVs, orphanBackend := createTcpVs(ctx, admin, 8002)
		instanceUuid, vipUuid := createInstance(ctx, admin, zone, keptVs, deletedVs)
		subscriber := connectSubscriber(ctx, controllerB, instanceUuid)
		Eventually(heldUuids(subscriber, vsType)).Should(ConsistOf(keptVs, deletedVs))
		Eventually(heldUuids(subscriber, backendType)).Should(ConsistOf(updatedBackend, orphanBackend))

		subscriber.Drop()
		subscriber.ClearHistory()
		addedVs, addedBackend := createTcpVs(ctx, admin, 8003)
		setVipVirtualServers(ctx, admin, vipUuid, keptVs, addedVs)
		updateBackendPort(ctx, admin, updatedBackend, 9090)
		deleteVsOnAExpectGoneOnB(ctx, admin, adminOnB, deletedVs)

		// with no agent left, controller B dropped the announcer of the instance: the reconnect builds a new one from
		// postgres, and a late nats event re-reads the same state, so its unchanged signature is skipped
		Expect(subscriber.Connect(ctx, controllerB.Addr(ctx))).To(Succeed())
		expectExactDelta(subscriber, map[string]typeDelta{
			vipType:     {Changed: []string{vipUuid}},
			vsType:      {Changed: []string{addedVs}, Removed: []string{deletedVs}},
			backendType: {Changed: []string{updatedBackend, addedBackend}, Removed: []string{orphanBackend}},
		})

		By("changing a backend on controller A once reconnected to controller B")
		subscriber.ClearHistory()
		updateBackendPort(ctx, admin, updatedBackend, 9091)
		expectExactDelta(subscriber, map[string]typeDelta{backendType: {Changed: []string{updatedBackend}}})
		Expect(heldBackendPort(subscriber, updatedBackend)()).To(BeEquivalentTo(9091))
		Expect(subscriber.Err()).NotTo(HaveOccurred())
	}, NodeTimeout(5*time.Minute))
})

// deleteVsOnAExpectGoneOnB proves the delete is effective on controller B through the shared store
func deleteVsOnAExpectGoneOnB(ctx context.Context, admin, adminOnB *env.Api, vsUuid string) {
	GinkgoHelper()
	_, err := admin.VS.DeleteVS(ctx, &svccore.UuidRequest{UUID: vsUuid})
	Expect(err).NotTo(HaveOccurred())
	_, err = adminOnB.VS.ReadVS(ctx, &svccore.UuidRequest{UUID: vsUuid})
	Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound), "%v", err)
}
