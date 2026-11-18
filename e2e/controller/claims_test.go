//go:build e2e

package e2e_test

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svclease "github.com/seagalia/flock/sdk/flock/services/lease/v1"
	"github.com/seagalia/flock/sdk/flock/services/lease/v1/svcleaseconnect"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"

	"github.com/seagalia/flock/e2e/controller/env"
	"github.com/seagalia/flock/e2e/controller/fakeagent"
)

var _ = Describe("Claims", func() {
	// on mysql a claim deleting a missing row locked the gap, and the claims of the streams of one agent deadlocked on
	// their inserts: the controller then logged "Deadlock found" and retried, or failed the stream
	It("claims nodes and leases concurrently on mysql without deadlock", func(ctx SpecContext) {
		const subscribers = 5
		const acquires = 10
		dsn, err := e2eEnv.StartMysql(ctx)
		Expect(err).NotTo(HaveOccurred())
		g := startController(ctx, env.ControllerOptions{
			Name: "controller-mysql-claims", Store: env.StoreMysql, DSN: dsn, Instantiators: env.SubscriberInstantiators(),
		})
		admin := loginAdmin(ctx, g)
		zoneResp, err := admin.Zone.CreateZone(ctx, &svczone.CreateZoneRequest{Zone: env.NewZone(env.SubscriberZone)})
		Expect(err).NotTo(HaveOccurred())
		vipResp, err := admin.VIP.CreateVIP(ctx, &svcvip.CreateVIPRequest{Vip: env.NewVip("claims-vip", "10.0.0.1")})
		Expect(err).NotTo(HaveOccurred())
		vipUuid := vipResp.GetVip().GetUUID()
		instResp, err := admin.Instance.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{
			Instance: env.NewInstance("claims-instance", zoneResp.GetZone().GetUUID(), vipUuid),
		})
		Expect(err).NotTo(HaveOccurred())
		instanceUuid := instResp.GetInstance().GetUUID()

		subscriberCtx, cancelSubscribers := context.WithCancel(context.Background())
		DeferCleanup(cancelSubscribers)
		connectErrs := make([]error, subscribers)
		var connects sync.WaitGroup
		for i := range subscribers {
			m := fakeagent.New(instanceUuid, e2eEnv.Certs.SubscriberTLSConfig(env.HostServerName))
			registerSubscriberForDump(instanceUuid, m)
			DeferCleanup(m.Drop)
			connects.Go(func() { connectErrs[i] = m.Connect(subscriberCtx, g.Addr(ctx)) })
		}
		connects.Wait()
		for _, err := range connectErrs {
			Expect(err).NotTo(HaveOccurred())
		}

		_, err = admin.Perm.AddPermission(ctx, &svcperm.AddPermissionRequest{
			ResourceType: vipType, ResourceUuid: vipUuid, Role: permission.Role_WRITER,
			Member: &permission.Member{MemberType: permission.MemberType_GROUP, MemberName: "agent-" + env.SubscriberName},
		})
		Expect(err).NotTo(HaveOccurred())
		clients := helpers.NewGrpcClients(e2eEnv.Certs.SubscriberTLSConfig(env.HostServerName))
		defer clients.Close()
		leases := svcleaseconnect.NewLeaseClient(clients.Client(g.Addr(ctx)))
		responses := make([]*svclease.AcquireResponse, acquires)
		acquireErrs := make([]error, acquires)
		var acquiring sync.WaitGroup
		for i := range acquires {
			acquiring.Go(func() {
				responses[i], acquireErrs[i] = leases.Acquire(ctx, &svclease.AcquireRequest{
					TypeUrl: vipType, UUID: vipUuid, InstanceUuid: instanceUuid,
				})
			})
		}
		acquiring.Wait()
		granted := 0
		for i := range acquires {
			Expect(acquireErrs[i]).NotTo(HaveOccurred())
			if responses[i].GetGranted() {
				granted++
			}
		}
		Expect(granted).To(Equal(1))

		Expect(g.Logs(ctx)).NotTo(Or(ContainSubstring("Deadlock found"), ContainSubstring("Error 1213")))
	}, NodeTimeout(10*time.Minute))
})
