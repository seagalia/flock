//go:build e2e

package e2e_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"github.com/samber/lo"
	"maps"
	"math/big"
	"slices"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svclease "github.com/seagalia/flock/sdk/flock/services/lease/v1"
	"github.com/seagalia/flock/sdk/flock/services/lease/v1/svcleaseconnect"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"

	"github.com/seagalia/flock/e2e/controller/env"
	"github.com/seagalia/flock/e2e/controller/fakeagent"
)

var (
	vipType     = helpers.TypeUrl[*vip.VIP]()
	vsType      = helpers.TypeUrl[*vs.VirtualServer]()
	backendType = helpers.TypeUrl[*backend.Backend]()
	certType    = helpers.TypeUrl[*cert.Certificate]()
)

// nothingElseArrives is how long an agent must receive nothing more once it holds what is expected
const nothingElseArrives = 2 * time.Second

var _ = Describe("Sync", func() {
	for _, store := range []env.Store{env.StoreSqlite, env.StorePostgres, env.StoreMysql} {
		Describe("on "+string(store), Ordered, func() {
			var (
				g     *env.Controller
				admin *env.Api
			)

			BeforeAll(func(ctx SpecContext) {
				opts := env.ControllerOptions{
					Name: "controller-sync-" + string(store), Store: store, Instantiators: env.SubscriberInstantiators(),
				}
				var err error
				switch store {
				case env.StorePostgres:
					opts.DSN, err = e2eEnv.StartPostgres(ctx)
				case env.StoreMysql:
					opts.DSN, err = e2eEnv.StartMysql(ctx)
				}
				Expect(err).NotTo(HaveOccurred())
				g = startController(ctx, opts)
				admin = loginAdmin(ctx, g)
			}, NodeTimeout(10*time.Minute))

			BeforeEach(func() {
				registerControllerForDump(g)
			})

			It("pushes live creates, updates and deletes of its instance", func(ctx SpecContext) {
				zone := subscriberZone(ctx, admin)
				firstVs, firstBackend := createTcpVs(ctx, admin, 8001)
				instanceUuid, firstVip := createInstance(ctx, admin, zone, firstVs)
				subscriber := connectSubscriber(ctx, g, instanceUuid)
				Eventually(heldUuids(subscriber, vipType)).Should(ConsistOf(firstVip))
				Eventually(heldUuids(subscriber, vsType)).Should(ConsistOf(firstVs))
				Eventually(heldUuids(subscriber, backendType)).Should(ConsistOf(firstBackend))

				By("linking a new vip with its own virtual server and backend to the instance")
				linkedVs, linkedBackend := createTcpVs(ctx, admin, 8001)
				linkedVip := createVip(ctx, admin, linkedVs)
				setInstanceVips(ctx, admin, instanceUuid, firstVip, linkedVip)
				Eventually(heldUuids(subscriber, vipType)).Should(ConsistOf(firstVip, linkedVip))
				Eventually(heldUuids(subscriber, vsType)).Should(ConsistOf(firstVs, linkedVs))
				Eventually(heldUuids(subscriber, backendType)).Should(ConsistOf(firstBackend, linkedBackend))

				By("attaching a new virtual server to the first vip")
				attachedVs, attachedBackend := createTcpVs(ctx, admin, 8002)
				setVipVirtualServers(ctx, admin, firstVip, firstVs, attachedVs)
				Eventually(heldUuids(subscriber, vsType)).Should(ConsistOf(firstVs, linkedVs, attachedVs))
				Eventually(heldUuids(subscriber, backendType)).Should(ConsistOf(firstBackend, linkedBackend, attachedBackend))
				Eventually(heldVipVirtualServers(subscriber, firstVip)).Should(ConsistOf(firstVs, attachedVs))

				By("updating a backend")
				updateBackendPort(ctx, admin, firstBackend, 9090)
				Eventually(heldBackendPort(subscriber, firstBackend)).Should(BeEquivalentTo(9090))

				By("detaching then deleting the attached virtual server")
				setVipVirtualServers(ctx, admin, firstVip, firstVs)
				Eventually(heldUuids(subscriber, vsType)).Should(ConsistOf(firstVs, linkedVs))
				Eventually(heldUuids(subscriber, backendType)).Should(ConsistOf(firstBackend, linkedBackend))
				_, err := admin.VS.DeleteVS(ctx, &svccore.UuidRequest{UUID: attachedVs})
				Expect(err).NotTo(HaveOccurred())
				_, err = admin.VS.ReadVS(ctx, &svccore.UuidRequest{UUID: attachedVs})
				Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound), "%v", err)

				By("dropping every vip from the instance")
				setInstanceVips(ctx, admin, instanceUuid)
				Eventually(heldUuids(subscriber, vipType)).Should(BeEmpty())
				Eventually(heldUuids(subscriber, vsType)).Should(BeEmpty())
				Eventually(heldUuids(subscriber, backendType)).Should(BeEmpty())
				Expect(subscriber.Err()).NotTo(HaveOccurred())
			}, NodeTimeout(5*time.Minute))

			It("pushes an http virtual server with its virtual host, certificate and backend", func(ctx SpecContext) {
				zone := subscriberZone(ctx, admin)
				tcpVs, tcpBackend := createTcpVs(ctx, admin, 8001)
				instanceUuid, vipUuid := createInstance(ctx, admin, zone, tcpVs)
				subscriber := connectSubscriber(ctx, g, instanceUuid)
				Eventually(heldUuids(subscriber, vsType)).Should(ConsistOf(tcpVs))

				By("attaching an http virtual server routed through a tls virtual host")
				certUuid := createCertificate(ctx, admin)
				httpVs, httpBackend := createHttpVs(ctx, admin, 8443, []*route.VirtualHost{env.NewTlsVirtualHost(certUuid)})
				setVipVirtualServers(ctx, admin, vipUuid, tcpVs, httpVs)
				Eventually(heldUuids(subscriber, vsType)).Should(ConsistOf(tcpVs, httpVs))
				Eventually(heldUuids(subscriber, certType)).Should(ConsistOf(certUuid))
				Eventually(heldUuids(subscriber, backendType)).Should(ConsistOf(tcpBackend, httpBackend))

				By("changing the port of the http virtual server in place")
				setVsPort(ctx, admin, httpVs, 8444)
				Eventually(heldVsPort(subscriber, httpVs)).Should(BeEquivalentTo(8444))

				By("detaching the http virtual server")
				setVipVirtualServers(ctx, admin, vipUuid, tcpVs)
				Eventually(heldUuids(subscriber, vsType)).Should(ConsistOf(tcpVs))
				Eventually(heldUuids(subscriber, certType)).Should(BeEmpty())
				Eventually(heldUuids(subscriber, backendType)).Should(ConsistOf(tcpBackend))
				Expect(subscriber.Err()).NotTo(HaveOccurred())
			}, NodeTimeout(5*time.Minute))

			It("sends nothing of another instance", func(ctx SpecContext) {
				zone := subscriberZone(ctx, admin)
				vsA, _ := createTcpVs(ctx, admin, 8001)
				instanceA, vipA := createInstance(ctx, admin, zone, vsA)
				vsB, backendB := createTcpVs(ctx, admin, 8001)
				instanceB, vipB := createInstance(ctx, admin, zone, vsB)
				subscriberA := connectSubscriber(ctx, g, instanceA)
				subscriberB := connectSubscriber(ctx, g, instanceB)
				Eventually(heldUuids(subscriberA, vipType)).Should(ConsistOf(vipA))
				Eventually(heldUuids(subscriberB, vipType)).Should(ConsistOf(vipB))

				By("changing every kind of resource of B while A is connected")
				attachedVsB, attachedBackendB := createTcpVs(ctx, admin, 8002)
				setVipVirtualServers(ctx, admin, vipB, vsB, attachedVsB)
				updateBackendPort(ctx, admin, backendB, 9090)
				linkedVsB, linkedBackendB := createTcpVs(ctx, admin, 8001)
				linkedVipB := createVip(ctx, admin, linkedVsB)
				setInstanceVips(ctx, admin, instanceB, vipB, linkedVipB)
				setVipVirtualServers(ctx, admin, vipB, vsB)
				// B holding the result proves controller announced every change, so A not having them is not only a slow controller
				Eventually(heldUuids(subscriberB, vsType)).Should(ConsistOf(vsB, linkedVsB))
				Eventually(heldBackendPort(subscriberB, backendB)).Should(BeEquivalentTo(9090))

				uuidsOfB := []string{
					vipB, linkedVipB, vsB, attachedVsB, linkedVsB, backendB, attachedBackendB, linkedBackendB,
				}
				// History is never trimmed, so it also shows a B resource sent to A then removed before this check
				Consistently(receivedUuids(subscriberA), nothingElseArrives).
					ShouldNot(ContainElement(BeElementOf(uuidsOfB)))
				Expect(subscriberA.Err()).NotTo(HaveOccurred())
			}, NodeTimeout(5*time.Minute))

			It("sends exactly the delta after a drop", func(ctx SpecContext) {
				zone := subscriberZone(ctx, admin)
				keptVs, updatedBackend := createTcpVs(ctx, admin, 8001)
				deletedVs, orphanBackend := createTcpVs(ctx, admin, 8002)
				instanceUuid, vipUuid := createInstance(ctx, admin, zone, keptVs, deletedVs)
				subscriber := connectSubscriber(ctx, g, instanceUuid)
				Eventually(heldUuids(subscriber, vipType)).Should(ConsistOf(vipUuid))
				Eventually(heldUuids(subscriber, vsType)).Should(ConsistOf(keptVs, deletedVs))
				Eventually(heldUuids(subscriber, backendType)).Should(ConsistOf(updatedBackend, orphanBackend))

				subscriber.Drop()
				subscriber.ClearHistory()
				addedVs, addedBackend := createTcpVs(ctx, admin, 8003)
				setVipVirtualServers(ctx, admin, vipUuid, keptVs, addedVs)
				updateBackendPort(ctx, admin, updatedBackend, 9090)
				_, err := admin.VS.DeleteVS(ctx, &svccore.UuidRequest{UUID: deletedVs})
				Expect(err).NotTo(HaveOccurred())

				Expect(subscriber.Connect(ctx, g.Addr(ctx))).To(Succeed())
				expectExactDelta(subscriber, map[string]typeDelta{
					vipType:     {Changed: []string{vipUuid}},
					vsType:      {Changed: []string{addedVs}, Removed: []string{deletedVs}},
					backendType: {Changed: []string{updatedBackend, addedBackend}, Removed: []string{orphanBackend}},
				})
				Expect(heldBackendPort(subscriber, updatedBackend)()).To(BeEquivalentTo(9090))

				By("changing a backend once reconnected")
				subscriber.ClearHistory()
				updateBackendPort(ctx, admin, updatedBackend, 9091)
				expectExactDelta(subscriber, map[string]typeDelta{backendType: {Changed: []string{updatedBackend}}})
				Expect(heldBackendPort(subscriber, updatedBackend)()).To(BeEquivalentTo(9091))
				Expect(subscriber.Err()).NotTo(HaveOccurred())
			}, NodeTimeout(5*time.Minute))

			It("sends the full state on a fresh reconnect", func(ctx SpecContext) {
				zone := subscriberZone(ctx, admin)
				firstVs, firstBackend := createTcpVs(ctx, admin, 8001)
				secondVs, secondBackend := createTcpVs(ctx, admin, 8002)
				instanceUuid, vipUuid := createInstance(ctx, admin, zone, firstVs, secondVs)
				subscriber := connectSubscriber(ctx, g, instanceUuid)
				Eventually(heldUuids(subscriber, vipType)).Should(ConsistOf(vipUuid))
				Eventually(heldUuids(subscriber, vsType)).Should(ConsistOf(firstVs, secondVs))
				Eventually(heldUuids(subscriber, backendType)).Should(ConsistOf(firstBackend, secondBackend))

				subscriber.Drop()
				subscriber.ForgetState()
				subscriber.ClearHistory()
				Expect(subscriber.Connect(ctx, g.Addr(ctx))).To(Succeed())

				expectExactDelta(subscriber, map[string]typeDelta{
					vipType:     {Changed: []string{vipUuid}},
					vsType:      {Changed: []string{firstVs, secondVs}},
					backendType: {Changed: []string{firstBackend, secondBackend}},
				})
				listed := listedSignatures(ctx, admin, instanceUuid)
				for _, typeUrl := range []string{vipType, vsType, backendType} {
					Expect(heldSignatures(subscriber, typeUrl)).To(Equal(listed[typeUrl]), typeUrl)
				}
				Expect(subscriber.Err()).NotTo(HaveOccurred())
			}, NodeTimeout(5*time.Minute))

			It("keeps a second agent of the same instance in sync while the first is away", func(ctx SpecContext) {
				zone := subscriberZone(ctx, admin)
				keptVs, updatedBackend := createTcpVs(ctx, admin, 8001)
				instanceUuid, vipUuid := createInstance(ctx, admin, zone, keptVs)
				away := connectSubscriber(ctx, g, instanceUuid)
				staying := connectSubscriber(ctx, g, instanceUuid)
				for _, subscriber := range []*fakeagent.Subscriber{away, staying} {
					expectHolds(subscriber, vipUuid, keptVs, updatedBackend)
				}

				away.Drop()
				away.ClearHistory()
				addedVs, addedBackend := createTcpVs(ctx, admin, 8002)
				setVipVirtualServers(ctx, admin, vipUuid, keptVs, addedVs)
				updateBackendPort(ctx, admin, updatedBackend, 9090)
				Eventually(heldUuids(staying, vsType)).Should(ConsistOf(keptVs, addedVs))
				Eventually(heldBackendPort(staying, addedBackend)).Should(BeEquivalentTo(8080))
				Eventually(heldBackendPort(staying, updatedBackend)).Should(BeEquivalentTo(9090))

				staying.ClearHistory()
				Expect(away.Connect(ctx, g.Addr(ctx))).To(Succeed())
				expectExactDelta(away, map[string]typeDelta{
					vipType:     {Changed: []string{vipUuid}},
					vsType:      {Changed: []string{addedVs}},
					backendType: {Changed: []string{updatedBackend, addedBackend}},
				})
				// the reconnect of an agent is no change for the other ones
				Expect(receivedDeltas(staying)).To(BeEmpty())
				Expect(away.Err()).NotTo(HaveOccurred())
				Expect(staying.Err()).NotTo(HaveOccurred())
			}, NodeTimeout(5*time.Minute))

			It("grants the lease of a vip to one holder at a time", func(ctx SpecContext) {
				vsUuid, _ := createTcpVs(ctx, admin, 8001)
				instanceUuid, vipUuid := createInstance(ctx, admin, subscriberZone(ctx, admin), vsUuid)
				connectSubscriber(ctx, g, instanceUuid)
				_, err := admin.Perm.AddPermission(ctx, &svcperm.AddPermissionRequest{
					ResourceType: vipType, ResourceUuid: vipUuid, Role: permission.Role_WRITER,
					Member: &permission.Member{MemberType: permission.MemberType_GROUP, MemberName: "agent-" + env.SubscriberName},
				})
				Expect(err).NotTo(HaveOccurred())

				clients := helpers.NewGrpcClients(e2eEnv.Certs.SubscriberTLSConfig(env.HostServerName))
				DeferCleanup(clients.Close)
				leases := svcleaseconnect.NewLeaseClient(clients.Client(g.Addr(ctx)))
				acquire := func() *svclease.AcquireResponse {
					GinkgoHelper()
					resp, err := leases.Acquire(ctx, &svclease.AcquireRequest{
						TypeUrl: vipType, UUID: vipUuid, InstanceUuid: instanceUuid,
					})
					Expect(err).NotTo(HaveOccurred())
					return resp
				}

				granted := acquire()
				Expect(granted.GetGranted()).To(BeTrue())
				again := acquire()
				Expect(again.GetGranted()).To(BeFalse())
				Expect(again.GetHolderName()).To(Equal(env.SubscriberName))

				_, err = leases.Renew(ctx, &svclease.RenewRequest{LeaseId: granted.GetLeaseId()})
				Expect(err).NotTo(HaveOccurred())
				_, err = leases.Release(ctx, &svclease.ReleaseRequest{LeaseId: granted.GetLeaseId()})
				Expect(err).NotTo(HaveOccurred())
				Expect(acquire().GetGranted()).To(BeTrue())
			}, NodeTimeout(5*time.Minute))
		})
	}
})

// subscriberZone gives the zone whose instances the subscriber may connect for, created by the first spec of the
// controller that needs it
func subscriberZone(ctx context.Context, admin *env.Api) string {
	GinkgoHelper()
	listResp, err := admin.Zone.ListZone(ctx, &svczone.ListZoneRequest{
		Filters: []*svccore.ListFilter{{Key: "name", Value: env.SubscriberZone}},
	})
	if err != nil {
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound), "%v", err)
	}
	if len(listResp.GetZones()) > 0 {
		return listResp.GetZones()[0].GetUUID()
	}
	resp, err := admin.Zone.CreateZone(ctx, &svczone.CreateZoneRequest{Zone: env.NewZone(env.SubscriberZone)})
	Expect(err).NotTo(HaveOccurred())
	return resp.GetZone().GetUUID()
}

func createTcpVs(ctx context.Context, admin *env.Api, port uint32) (vsUuid, backendUuid string) {
	GinkgoHelper()
	backendResp, err := admin.Backend.CreateBackend(ctx, &svcbackend.CreateBackendRequest{
		Backend: env.NewBackend(env.Unique("backend")),
	})
	Expect(err).NotTo(HaveOccurred())
	backendUuid = backendResp.GetBackend().GetUUID()
	vsResp, err := admin.VS.CreateVS(ctx, &svcvs.CreateVSRequest{
		Vs: env.NewTcpVs(env.Unique("vs"), port, backendUuid),
	})
	Expect(err).NotTo(HaveOccurred())
	return vsResp.GetVs().GetUUID(), backendUuid
}

func createHttpVs(ctx context.Context, admin *env.Api, port uint32, vhosts []*route.VirtualHost) (vsUuid, backendUuid string) {
	GinkgoHelper()
	backendResp, err := admin.Backend.CreateBackend(ctx, &svcbackend.CreateBackendRequest{
		Backend: env.NewBackend(env.Unique("backend")),
	})
	Expect(err).NotTo(HaveOccurred())
	backendUuid = backendResp.GetBackend().GetUUID()
	// the backend is used by the virtual server through the routes of its virtual hosts
	for _, vhost := range vhosts {
		vhost.Routes = append(vhost.Routes, &route.Route{
			Name:   "default",
			Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
			Action: &route.Route_BackendUuid{BackendUuid: backendUuid},
		})
	}
	vsResp, err := admin.VS.CreateVS(ctx, &svcvs.CreateVSRequest{
		Vs: env.NewHttpVs(env.Unique("vs"), port, vhosts),
	})
	Expect(err).NotTo(HaveOccurred())
	return vsResp.GetVs().GetUUID(), backendUuid
}

func createCertificate(ctx context.Context, admin *env.Api) string {
	GinkgoHelper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "vhost.e2e.local"},
		DNSNames:     []string{"vhost.e2e.local"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDer, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())
	keyDer, err := x509.MarshalECPrivateKey(key)
	Expect(err).NotTo(HaveOccurred())
	certPem := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDer}))
	keyPem := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}))
	resp, err := admin.Cert.CreateCertificate(ctx, &svccert.CreateCertificateRequest{Certificate: &cert.Certificate{
		Name:       env.Unique("cert"),
		CertChain:  certPem,
		PrivateKey: keyPem,
	}})
	Expect(err).NotTo(HaveOccurred())
	return resp.GetCertificate().GetUUID()
}

var lastVipHost atomic.Uint32

func createVip(ctx context.Context, admin *env.Api, vsUuids ...string) string {
	GinkgoHelper()
	ip := fmt.Sprintf("10.0.0.%d", lastVipHost.Add(1))
	vipResp, err := admin.VIP.CreateVIP(ctx, &svcvip.CreateVIPRequest{Vip: env.NewVip(env.Unique("vip"), ip, vsUuids...)})
	Expect(err).NotTo(HaveOccurred())
	return vipResp.GetVip().GetUUID()
}

func createInstance(ctx context.Context, admin *env.Api, zoneUuid string, vsUuids ...string) (instanceUuid, vipUuid string) {
	GinkgoHelper()
	vipUuid = createVip(ctx, admin, vsUuids...)
	instResp, err := admin.Instance.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{
		Instance: env.NewInstance(env.Unique("inst"), zoneUuid, vipUuid),
	})
	Expect(err).NotTo(HaveOccurred())
	return instResp.GetInstance().GetUUID(), vipUuid
}

func setInstanceVips(ctx context.Context, admin *env.Api, instanceUuid string, vipUuids ...string) {
	GinkgoHelper()
	instResp, err := admin.Instance.ReadInstance(ctx, &svccore.UuidRequest{UUID: instanceUuid})
	Expect(err).NotTo(HaveOccurred())
	// the vips of an instance change one by one, update-instance refuses them
	removed, added := lo.Difference(instResp.GetInstance().GetVipsUuid(), vipUuids)
	for _, vipUuid := range removed {
		_, err = admin.Instance.DeleteVipFromInstance(ctx, &svcinstance.DeleteVipFromInstanceRequest{UUID: instanceUuid, VipUuid: vipUuid})
		Expect(err).NotTo(HaveOccurred())
	}
	for _, vipUuid := range added {
		_, err = admin.Instance.AddVipToInstance(ctx, &svcinstance.AddVipToInstanceRequest{UUID: instanceUuid, VipUuid: vipUuid})
		Expect(err).NotTo(HaveOccurred())
	}
}

func setVipVirtualServers(ctx context.Context, admin *env.Api, vipUuid string, vsUuids ...string) {
	GinkgoHelper()
	vipResp, err := admin.VIP.ReadVIP(ctx, &svccore.UuidRequest{UUID: vipUuid})
	Expect(err).NotTo(HaveOccurred())
	updated := vipResp.GetVip()
	updated.VirtualServersUuid = vsUuids
	_, err = admin.VIP.UpdateVIP(ctx, &svcvip.UpdateVIPRequest{UUID: vipUuid, Vip: updated})
	Expect(err).NotTo(HaveOccurred())
}

func updateBackendPort(ctx context.Context, admin *env.Api, backendUuid string, port uint32) {
	GinkgoHelper()
	backendResp, err := admin.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
	Expect(err).NotTo(HaveOccurred())
	updated := backendResp.GetBackend()
	updated.GetIpAddrGroup().GetIpAddresses()[0].Port = port
	_, err = admin.Backend.UpdateBackend(ctx, &svcbackend.UpdateBackendRequest{UUID: backendUuid, Backend: updated})
	Expect(err).NotTo(HaveOccurred())
}

func setVsPort(ctx context.Context, admin *env.Api, vsUuid string, port uint32) {
	GinkgoHelper()
	vsResp, err := admin.VS.ReadVS(ctx, &svccore.UuidRequest{UUID: vsUuid})
	Expect(err).NotTo(HaveOccurred())
	updated := vsResp.GetVs()
	updated.Port = port
	_, err = admin.VS.UpdateVS(ctx, &svcvs.UpdateVSRequest{UUID: vsUuid, Vs: updated})
	Expect(err).NotTo(HaveOccurred())
}

// expectHolds waits until m holds the vip, the vs and the backend: each type has its own stream, a subscriber dropped
// before a stream answered would ask for everything of that type again on its reconnect
func expectHolds(m *fakeagent.Subscriber, vipUuid, vsUuid, backendUuid string) {
	GinkgoHelper()
	Eventually(heldUuids(m, vipType)).Should(ConsistOf(vipUuid))
	Eventually(heldUuids(m, vsType)).Should(ConsistOf(vsUuid))
	Eventually(heldUuids(m, backendType)).Should(ConsistOf(backendUuid))
}

// heldUuids is polled by Eventually, so it reads the agent state on each call
func heldUuids(m *fakeagent.Subscriber, typeUrl string) func() []string {
	return func() []string {
		return slices.Sorted(maps.Keys(m.State(typeUrl)))
	}
}

func heldVipVirtualServers(m *fakeagent.Subscriber, vipUuid string) func() []string {
	return func() []string {
		held := &vip.VIP{}
		if m.State(vipType)[vipUuid].ToMessage(held) != nil {
			return nil
		}
		return held.GetVirtualServersUuid()
	}
}

func heldBackendPort(m *fakeagent.Subscriber, backendUuid string) func() uint32 {
	return func() uint32 {
		held := &backend.Backend{}
		if m.State(backendType)[backendUuid].ToMessage(held) != nil {
			return 0
		}
		addresses := held.GetIpAddrGroup().GetIpAddresses()
		if len(addresses) == 0 {
			return 0
		}
		return addresses[0].GetPort()
	}
}

func heldVsPort(m *fakeagent.Subscriber, vsUuid string) func() uint32 {
	return func() uint32 {
		held := &vs.VirtualServer{}
		if m.State(vsType)[vsUuid].ToMessage(held) != nil {
			return 0
		}
		return held.GetPort()
	}
}

func heldSignatures(m *fakeagent.Subscriber, typeUrl string) map[string]string {
	signatures := make(map[string]string)
	for uuid, res := range m.State(typeUrl) {
		signatures[uuid] = res.GetSignature()
	}
	return signatures
}

// listedSignatures walks the instance chain through the api: instance -> vips -> virtual servers -> backends
func listedSignatures(ctx context.Context, admin *env.Api, instanceUuid string) map[string]map[string]string {
	GinkgoHelper()
	listed := map[string]map[string]string{vipType: {}, vsType: {}, backendType: {}}
	sign := func(typeUrl string, msg proto.Message) {
		signature, err := helpers.MessageSignature(msg)
		Expect(err).NotTo(HaveOccurred())
		listed[typeUrl][helpers.GetIdentifier(msg)] = signature
	}
	instResp, err := admin.Instance.ReadInstance(ctx, &svccore.UuidRequest{UUID: instanceUuid})
	Expect(err).NotTo(HaveOccurred())
	for _, vipUuid := range instResp.GetInstance().GetVipsUuid() {
		vipResp, err := admin.VIP.ReadVIP(ctx, &svccore.UuidRequest{UUID: vipUuid})
		Expect(err).NotTo(HaveOccurred())
		sign(vipType, vipResp.GetVip())
		for _, vsUuid := range vipResp.GetVip().GetVirtualServersUuid() {
			vsResp, err := admin.VS.ReadVS(ctx, &svccore.UuidRequest{UUID: vsUuid})
			Expect(err).NotTo(HaveOccurred())
			sign(vsType, vsResp.GetVs())
			for _, backendUuid := range vsResp.GetVs().GetBackendUuids() {
				backendResp, err := admin.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
				Expect(err).NotTo(HaveOccurred())
				sign(backendType, backendResp.GetBackend())
			}
		}
	}
	return listed
}

// receivedUuids is every uuid sent to the agent on any type, as a resource or as a removal
func receivedUuids(m *fakeagent.Subscriber) func() []string {
	return func() []string {
		var uuids []string
		for _, delta := range receivedDeltas(m) {
			uuids = append(uuids, delta.Changed...)
			uuids = append(uuids, delta.Removed...)
		}
		return uuids
	}
}

type typeDelta struct {
	Changed []string
	Removed []string
}

// receivedDeltas merges every response recorded per type: controller may split one delta over several responses,
// and a uuid sent twice stays twice so that it fails the comparison
func receivedDeltas(m *fakeagent.Subscriber) map[string]typeDelta {
	deltas := make(map[string]typeDelta)
	for _, typeUrl := range fakeagent.SubscriberTypeUrls {
		history := m.History(typeUrl)
		if len(history) == 0 {
			continue
		}
		var delta typeDelta
		for _, resp := range history {
			for _, res := range resp.GetResources() {
				delta.Changed = append(delta.Changed, res.GetUuid())
			}
			delta.Removed = append(delta.Removed, resp.GetRemovedResources()...)
		}
		slices.Sort(delta.Changed)
		slices.Sort(delta.Removed)
		deltas[typeUrl] = delta
	}
	return deltas
}

// expectExactDelta needs expected to list every type that must receive something, the others must receive nothing
func expectExactDelta(m *fakeagent.Subscriber, expected map[string]typeDelta) {
	GinkgoHelper()
	for typeUrl, delta := range expected {
		slices.Sort(delta.Changed)
		slices.Sort(delta.Removed)
		expected[typeUrl] = delta
	}
	received := func() map[string]typeDelta { return receivedDeltas(m) }
	Eventually(received).Should(Equal(expected))
	Consistently(received, nothingElseArrives).Should(Equal(expected))
}
