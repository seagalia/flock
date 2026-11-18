//go:build e2e

package e2e_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/e2e/controller/env"
)

// controllerRestartTimeout covers a full stop and boot of the controller container
const controllerRestartTimeout = 60 * time.Second

// Restart runs on postgres only: the sqlite store lives in the container and goes with it
var _ = Describe("Restart", func() {
	It("reconnects to the new address after a controller restart", func(ctx SpecContext) {
		dsn, err := e2eEnv.StartPostgres(ctx)
		Expect(err).NotTo(HaveOccurred())
		g := startController(ctx, env.ControllerOptions{
			Name: "controller-restart", Store: env.StorePostgres, DSN: dsn, Instantiators: env.SubscriberInstantiators(),
		})
		admin := loginAdmin(ctx, g)

		zone := subscriberZone(ctx, admin)
		vsUuid, backendUuid := createTcpVs(ctx, admin, 8001)
		instanceUuid, vipUuid := createInstance(ctx, admin, zone, vsUuid)
		subscriber := connectSubscriber(ctx, g, instanceUuid)
		Eventually(heldUuids(subscriber, vipType)).Should(ConsistOf(vipUuid))
		Eventually(heldUuids(subscriber, vsType)).Should(ConsistOf(vsUuid))
		Eventually(heldUuids(subscriber, backendType)).Should(ConsistOf(backendUuid))

		subscriber.Drop()
		subscriber.ClearHistory()
		addrBefore := g.Addr(ctx)
		Expect(g.Restart(ctx)).To(Succeed())
		GinkgoWriter.Printf("controller moved from %s to %s\n", addrBefore, g.Addr(ctx))

		// controller may still be binding its port right after the container started
		Eventually(func() error { return subscriber.Connect(ctx, g.Addr(ctx)) }, controllerRestartTimeout).Should(Succeed())
		Consistently(receivedUuids(subscriber), nothingElseArrives).Should(BeEmpty())
		Expect(subscriber.Err()).NotTo(HaveOccurred())

		By("changing a backend after the restart")
		// the token survives the restart, but the connection of admin points at the old port
		adminAfter := loginAdmin(ctx, g)
		subscriber.ClearHistory()
		updateBackendPort(ctx, adminAfter, backendUuid, 9090)
		expectExactDelta(subscriber, map[string]typeDelta{
			backendType: {Changed: []string{backendUuid}},
		})
		Expect(heldBackendPort(subscriber, backendUuid)()).To(BeEquivalentTo(9090))
		Expect(subscriber.Err()).NotTo(HaveOccurred())
	}, NodeTimeout(10*time.Minute))
})
