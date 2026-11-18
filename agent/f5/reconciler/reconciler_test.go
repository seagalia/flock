package reconciler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"google.golang.org/protobuf/proto"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/agent/f5/bigip"
	"github.com/seagalia/flock/agent/f5/bigip/bigipmock"
	"github.com/seagalia/flock/agent/f5/reconciler"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

const (
	instanceUUID = "inst-1"
	vipUUID      = "11111111-1111-1111-1111-111111111111"
	vsUUID       = "22222222-2222-2222-2222-222222222222"
	backendUUID  = "33333333-3333-3333-3333-333333333333"
)

// fakeEndpoints plays the backend handler: a backend has endpoints once discovered.
type fakeEndpoints struct {
	mu        sync.Mutex
	endpoints map[string][]*core.IPAddr
}

func (f *fakeEndpoints) Endpoints(backendUUID string) ([]*core.IPAddr, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	eps, ok := f.endpoints[backendUUID]
	return eps, ok
}

func (f *fakeEndpoints) discover(backendUUID string, ips ...string) { //nolint:unparam // the call sites show the value
	f.mu.Lock()
	defer f.mu.Unlock()
	eps := make([]*core.IPAddr, 0, len(ips))
	for _, ip := range ips {
		eps = append(eps, &core.IPAddr{Ip: ip, Port: 8080})
	}
	f.endpoints[backendUUID] = eps
}

func newVIP(uuid, ip, partition string, vsUUIDs ...string) *vip.VIP {
	v := &vip.VIP{UUID: uuid, Name: "vip-" + uuid[:4], Ip: ip, VirtualServersUuid: vsUUIDs}
	if partition != "" {
		v.Metadata = map[string]string{"agent.f5.partition": partition}
	}
	return v
}

func tcpVS(uuid string, port uint32, backendUUID string) *vs.VirtualServer { //nolint:unparam // the call sites show the value
	return &vs.VirtualServer{UUID: uuid, Name: "vs-" + uuid[:4], Port: port, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
		TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: backendUUID},
	}}
}

func newBackend(uuid string) *backend.Backend { //nolint:unparam // the call sites show the value
	return &backend.Backend{UUID: uuid, Name: "backend-" + uuid[:4], Endpoint: &backend.Backend_Eds{
		Eds: &backend.EDS{ServiceName: "svc"},
	}}
}

func store[T proto.Message](messages ...T) {
	for _, m := range messages {
		Expect(stores.StoreBy[T](rt.Store).Create(context.Background(), m)).To(Succeed())
	}
}

func unstore[T proto.Message](uuid string) {
	Expect(stores.StoreBy[T](rt.Store).Delete(context.Background(), uuid)).To(Succeed())
}

func key[T proto.Message](uuid string) (string, string) {
	return helpers.TypeUrl[T](), uuid
}

func declaredTenants(device *bigipmock.Mock) []string {
	names := make([]string, 0, len(device.Declares()))
	for _, d := range device.Declares() {
		names = append(names, d.Tenant)
	}
	return names
}

var _ = Describe("Reconciler", func() {
	var (
		device     *bigipmock.Mock
		endpoints  *fakeEndpoints
		stateStore *stores.StateStore
		opts       reconciler.Options
		rec        *reconciler.Reconciler
		cancel     context.CancelFunc
	)

	start := func() {
		client := bigip.New(bigip.Options{
			URLs: []string{device.URL()}, Username: bigipmock.Username, Password: bigipmock.Password,
			TLSConfig: device.TLSConfig(), RequestTimeout: 5 * time.Second, PollInterval: 5 * time.Millisecond,
			FailoverCacheTTL: -1,
		})
		rec = reconciler.New(opts, client, endpoints, rt.Store, stateStore, rt.SecretLinks)
		var ctx context.Context
		ctx, cancel = context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			_ = rec.Run(ctx)
		}()
		DeferCleanup(func() {
			cancel()
			Eventually(done).Should(BeClosed())
		})
	}

	simple := func(partition string) {
		store(newBackend(backendUUID))
		endpoints.discover(backendUUID, "10.0.0.10")
		store(tcpVS(vsUUID, 443, backendUUID))
		store(newVIP(vipUUID, "10.99.0.1", partition, vsUUID))
	}

	BeforeEach(func() {
		device = bigipmock.New()
		DeferCleanup(device.Close)
		endpoints = &fakeEndpoints{endpoints: map[string][]*core.IPAddr{}}
		stateStore = stores.NewStateStore()
		opts = reconciler.Options{
			InstanceUUID:      instanceUUID,
			DefaultPartition:  "flock",
			PartitionKey:      "agent.f5.partition",
			Debounce:          50 * time.Millisecond,
			MaxDebounce:       200 * time.Millisecond,
			MaxIncomplete:     time.Hour,
			ResyncInterval:    time.Hour,
			GarbageAfterStart: time.Hour,
			RetryMin:          10 * time.Millisecond,
			RetryMax:          50 * time.Millisecond,
		}
	})

	It("posts a tenant once after a burst of changes", func() {
		start()
		simple("")
		for range 10 {
			rec.Changed()
		}

		Eventually(device.Declares).Should(HaveLen(1))
		Consistently(device.Declares, 300*time.Millisecond).Should(HaveLen(1))
		Expect(device.Tenants()).To(HaveKey("flock"))
		Expect(rec.Status().PostedVIPs).To(HaveLen(1))
	})

	It("keeps the posted vips with their secret references, whatever the links become", func() {
		start()
		simple("")
		posted := newVIP(vipUUID, "10.99.0.1", "", vsUUID)
		posted.Metadata = map[string]string{"a": "v"}
		store(posted)
		vipType := helpers.TypeUrl[*vip.VIP]()
		rt.SecretLinks.Set(vipType, vipUUID, []secret.FoundRef{{Path: `metadata["a"]`, Ref: secret.Ref{SecretUUID: "s1", Key: "k"}}})
		rec.Changed()
		Eventually(func() []*vip.VIP { return rec.Status().PostedVIPs }).Should(HaveLen(1))

		rt.SecretLinks.Delete(vipType, vipUUID)

		Expect(rec.Status().PostedVIPs[0].GetMetadata()).To(Equal(map[string]string{"a": "secret://s1/k"}))
	})

	It("posts within max debounce under constant changes", func() {
		start()
		simple("")
		stop := time.After(400 * time.Millisecond)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
	loop:
		for {
			select {
			case <-ticker.C:
				rec.Changed()
			case <-stop:
				break loop
			}
		}

		Expect(device.Declares()).ToNot(BeEmpty())
	})

	It("posts nothing when nothing changed", func() {
		start()
		simple("")
		rec.Changed()
		Eventually(device.Declares).Should(HaveLen(1))

		rec.Changed()

		Consistently(device.Declares, 300*time.Millisecond).Should(HaveLen(1))
	})

	It("does not post an incomplete tenant", func() {
		start()
		store(newBackend(backendUUID))
		endpoints.discover(backendUUID, "10.0.0.10")
		store(newVIP(vipUUID, "10.99.0.1", "", vsUUID))
		rec.Changed()
		Consistently(device.Declares, 300*time.Millisecond).Should(BeEmpty())

		store(tcpVS(vsUUID, 443, backendUUID))
		rec.Changed()

		Eventually(device.Declares).Should(HaveLen(1))
	})

	It("posts an incomplete tenant after max incomplete and warns", func() {
		opts.MaxIncomplete = 200 * time.Millisecond
		start()
		store(newVIP(vipUUID, "10.99.0.1", "", vsUUID))
		rec.Changed()

		Eventually(device.Declares).Should(HaveLen(1))
		Eventually(func() []*flocktype.Note { return stateStore.Notes(key[*vip.VIP](vipUUID)) }).Should(ContainElement(
			f5Note("f5.post", "virtual server "+vsUUID+" is not known yet, the VIP is posted without it")))
	})

	It("waits for the first discovery of a backend", func() {
		start()
		store(newBackend(backendUUID))
		store(tcpVS(vsUUID, 443, backendUUID))
		store(newVIP(vipUUID, "10.99.0.1", "", vsUUID))
		rec.Changed()
		Consistently(device.Declares, 300*time.Millisecond).Should(BeEmpty())

		endpoints.discover(backendUUID, "10.0.0.10")
		rec.Changed()

		Eventually(device.Declares).Should(HaveLen(1))
	})

	It("posts a static backend without waiting for a discovery", func() {
		start()
		store(&backend.Backend{UUID: backendUUID, Name: "static", Endpoint: &backend.Backend_IpAddrGroup{
			IpAddrGroup: &core.IPAddrGroup{IpAddresses: []*core.IPAddr{{Ip: "10.0.0.10", Port: 8080}}},
		}})
		store(tcpVS(vsUUID, 443, backendUUID))
		store(newVIP(vipUUID, "10.99.0.1", "", vsUUID))
		rec.Changed()

		Eventually(device.Declares).Should(HaveLen(1))
	})

	It("puts a VIP in the partition of its metadata", func() {
		start()
		simple("team1")
		rec.Changed()

		Eventually(func() []string { return declaredTenants(device) }).Should(Equal([]string{"team1"}))
		Eventually(device.Tenants).Should(HaveKey("team1"))
		Expect(device.Tenants()).ToNot(HaveKey("flock"))
	})

	It("refuses an invalid partition name", func() {
		start()
		simple("Common")
		rec.Changed()

		Eventually(func() agentstatus.StatusResponse_State {
			_, state, _ := stateStore.Get(key[*vip.VIP](vipUUID))
			return state
		}).Should(Equal(agentstatus.StatusResponse_ERRORED))
		Expect(device.Declares()).To(BeEmpty())
	})

	It("posts the tenant losing a VIP first", func() {
		start()
		store(newVIP("44444444-4444-4444-4444-444444444444", "10.99.0.2", "teamA"))
		simple("teamA")
		rec.Changed()
		Eventually(device.Tenants).Should(HaveKey("teamA"))
		device.ResetDeclares()

		store(newVIP(vipUUID, "10.99.0.1", "teamB", vsUUID))
		rec.Changed()

		Eventually(func() []string { return declaredTenants(device) }).Should(Equal([]string{"teamA", "teamB"}))
		Eventually(func() []*flocktype.Note { return stateStore.Notes(key[*vip.VIP](vipUUID)) }).Should(ContainElement(
			f5Note("f5.post", "moved from partition teamA, traffic was interrupted during the move")))
	})

	It("deletes a tenant with no VIP left", func() {
		start()
		simple("")
		rec.Changed()
		Eventually(device.Tenants).Should(HaveKey("flock"))

		unstore[*vip.VIP](vipUUID)
		rec.Changed()

		Eventually(device.Tenants).ShouldNot(HaveKey("flock"))
		// the pass publishes its status after the delete the device already shows
		Eventually(func() []*vip.VIP { return rec.Status().PostedVIPs }).Should(BeEmpty())
	})

	It("refuses a partition it does not own", func() {
		device.AddPartition("team1")
		start()
		simple("team1")
		rec.Changed()

		Eventually(func() error {
			_, _, err := stateStore.Get(key[*vip.VIP](vipUUID))
			return err
		}).Should(MatchError(ContainSubstring("partition team1 exists and is not managed by this flock instance")))
		Expect(device.Declares()).To(BeEmpty())
	})

	It("refuses an AS3 tenant declared by someone else", func() {
		device.AddTenant("team1", "someone else")
		start()
		simple("team1")
		rec.Changed()

		Eventually(func() error {
			_, _, err := stateStore.Get(key[*vip.VIP](vipUUID))
			return err
		}).Should(MatchError(ContainSubstring("partition team1 exists and is not managed by this flock instance")))
		Expect(device.Declares()).To(BeEmpty())
	})

	It("marks the tenant resources ERRORED on a 422 and retries on a later change", func() {
		device.FailNextTask(bigipmock.NotLicensedResult)
		start()
		simple("")
		rec.Changed()

		Eventually(func() error {
			_, _, err := stateStore.Get(key[*vs.VirtualServer](vsUUID))
			return err
		}).Should(MatchError(ContainSubstring("not licensed")))
		Expect(rec.Status().FailedTenants).To(HaveKey("flock"))
		Consistently(device.Declares, 200*time.Millisecond).Should(HaveLen(1))

		store(tcpVS(vsUUID, 8443, backendUUID))
		rec.Changed()

		Eventually(device.Declares).Should(HaveLen(2))
		Eventually(func() map[string]string { return rec.Status().FailedTenants }).Should(BeEmpty())
		_, state, _ := stateStore.Get(key[*vs.VirtualServer](vsUUID))
		Expect(state).ToNot(Equal(agentstatus.StatusResponse_ERRORED))
	})

	It("reposts a refused tenant on resync", func() {
		opts.ResyncInterval = 300 * time.Millisecond
		device.FailNextTask(bigipmock.NotLicensedResult)
		start()
		simple("")
		rec.Changed()
		Eventually(func() map[string]string { return rec.Status().FailedTenants }).Should(HaveKey("flock"))

		Eventually(func() map[string]string { return rec.Status().FailedTenants }, 2*time.Second).Should(BeEmpty())
		Expect(device.Tenants()).To(HaveKey("flock"))
	})

	It("posts a moved VIP only once the partition it leaves released it", func() {
		start()
		store(newVIP("44444444-4444-4444-4444-444444444444", "10.99.0.2", "teamA"))
		simple("teamA")
		rec.Changed()
		Eventually(device.Tenants).Should(HaveKey("teamA"))
		device.ResetDeclares()
		device.SetBusy(1)

		store(newVIP(vipUUID, "10.99.0.1", "teamB", vsUUID))
		rec.Changed()

		Eventually(func() []string { return declaredTenants(device) }).Should(Equal([]string{"teamA", "teamA", "teamB"}))
	})

	It("shows the resources of a tenant ADDING until it is posted", func() {
		start()
		store(newBackend(backendUUID))
		endpoints.discover(backendUUID, "10.0.0.10")
		store(newVIP(vipUUID, "10.99.0.1", "", vsUUID))
		rec.Changed()
		Eventually(func() agentstatus.StatusResponse_State {
			_, state, _ := stateStore.Get(key[*vip.VIP](vipUUID))
			return state
		}).Should(Equal(agentstatus.StatusResponse_ADDING))

		store(tcpVS(vsUUID, 443, backendUUID))
		rec.Changed()

		Eventually(device.Tenants).Should(HaveKey("flock"))
		Eventually(func() agentstatus.StatusResponse_State {
			_, state, _ := stateStore.Get(key[*vip.VIP](vipUUID))
			return state
		}).Should(Equal(agentstatus.StatusResponse_OK))
	})

	It("does not sweep before the controller delivered the resources", func() {
		opts.GarbageAfterStart = 100 * time.Millisecond
		device.AddTenant("old", as3.TenantRemark(instanceUUID))
		start()

		Consistently(device.Tenants, 400*time.Millisecond).Should(HaveKey("old"))
		rec.Synced()
		Eventually(device.Tenants).ShouldNot(HaveKey("old"))
	})

	It("retries a busy device with backoff", func() {
		device.SetBusy(2)
		start()
		simple("")
		rec.Changed()

		Eventually(device.Tenants).Should(HaveKey("flock"))
		Expect(device.Declares()).To(HaveLen(3))
		_, state, _ := stateStore.Get(key[*vip.VIP](vipUUID))
		Expect(state).ToNot(Equal(agentstatus.StatusResponse_ERRORED))
	})

	It("sweeps orphan tenants after the garbage delay", func() {
		opts.GarbageAfterStart = 200 * time.Millisecond
		device.AddTenant("old", as3.TenantRemark(instanceUUID))
		device.AddTenant("other", "someone else")
		start()
		rec.Synced()

		Eventually(device.Tenants).ShouldNot(HaveKey("old"))
		Expect(device.Tenants()).To(HaveKey("other"))
	})

	It("reposts everything on resync and warns on drift", func() {
		opts.ResyncInterval = 300 * time.Millisecond
		start()
		simple("")
		rec.Changed()
		Eventually(device.Tenants).Should(HaveKey("flock"))

		device.AddTenant("flock", "edited by hand")

		Eventually(func() []*flocktype.Note { return stateStore.Notes(key[*vip.VIP](vipUUID)) }, 2*time.Second).Should(ContainElement(
			f5Note("f5.post", "configuration drift corrected on the device")))
	})

	It("sends a dry-run when asked", func() {
		opts.DryRun = true
		start()
		simple("")
		rec.Changed()

		Eventually(device.Declares).Should(HaveLen(1))
		Expect(device.Declares()[0].Action).To(Equal("dry-run"))
	})

	It("reports the render approximations and clears them", func() {
		start()
		simple("")
		v, _ := rt.Store.VirtualServers().Read(context.Background(), vsUUID)
		v.EnableL3Log = true
		store(v)
		rec.Changed()
		Eventually(func() []*flocktype.Note { return stateStore.Notes(key[*vs.VirtualServer](vsUUID)) }).Should(ContainElement(
			f5Note("f5.approximation", "enable_l3_log is not supported by the F5 agent, it is ignored")))

		v.EnableL3Log = false
		store(v)
		rec.Changed()

		Eventually(func() []*flocktype.Note { return stateStore.Notes(key[*vs.VirtualServer](vsUUID)) }).Should(BeEmpty())
	})

	It("gives the pools built from a backend", func() {
		start()
		simple("")
		rec.Changed()

		Eventually(func() []as3.PoolRef { return rec.PoolsOfBackend(backendUUID) }).Should(Equal([]as3.PoolRef{{
			BackendUUID: backendUUID, Tenant: "flock", App: as3.AppName(vipUUID), Pool: as3.PoolName(backendUUID),
		}}))
	})

	It("keeps going when the device is unreachable", func() {
		device.FailNext(http.StatusBadGateway, `{"code":502,"message":"bad gateway"}`)
		start()
		simple("")
		rec.Changed()

		Eventually(device.Tenants).Should(HaveKey("flock"))
		var tenant map[string]any
		Expect(json.Unmarshal(device.Tenants()["flock"], &tenant)).To(Succeed())
		Expect(tenant["remark"]).To(Equal(as3.TenantRemark(instanceUUID)))
	})
})

// f5Note matches a warning note of the F5 agent
func f5Note(code, message string) types.GomegaMatcher {
	return SatisfyAll(
		HaveField("Level", flocktype.Note_WARNING),
		HaveField("Code", code),
		HaveField("Message", message),
	)
}
