//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/seagalia/flock/e2e/controller/env"
)

const remoteIpamToken = "Bearer e2e-ipam-token"

var _ = Describe("IPAM", func() {
	Describe("internal", Ordered, func() {
		var (
			g     *env.Controller
			admin *env.Api
		)

		BeforeAll(func(ctx SpecContext) {
			g = startController(ctx, env.ControllerOptions{Name: "controller-ipam", Store: env.StoreSqlite})
			admin = loginAdmin(ctx, g)
		}, NodeTimeout(10*time.Minute))

		BeforeEach(func() {
			registerControllerForDump(g)
		})

		It("gives each registered vip its own ip of the zone pool", func(ctx SpecContext) {
			zoneUuid := createRangeZone(ctx, admin, "10.20.0.1", "10.20.0.3")

			first := registerVip(ctx, admin, zoneUuid)
			second := registerVip(ctx, admin, zoneUuid)

			Expect([]string{first.GetIp(), second.GetIp()}).To(ConsistOf("10.20.0.1", "10.20.0.2"))
			Expect(first.GetPrefixLength()).To(BeEquivalentTo(32))
			stored, err := admin.VIP.ReadVIP(ctx, &svccore.UuidRequest{UUID: first.GetUUID()})
			Expect(err).NotTo(HaveOccurred())
			Expect(stored.GetVip().GetIp()).To(Equal(first.GetIp()))
		}, NodeTimeout(time.Minute))

		It("refuses a vip once the pool is exhausted and reuses the ip of a deleted vip", func(ctx SpecContext) {
			zoneUuid := createRangeZone(ctx, admin, "10.21.0.1", "10.21.0.2")
			first := registerVip(ctx, admin, zoneUuid)
			registerVip(ctx, admin, zoneUuid)

			_, err := admin.VIP.RegisterVip(ctx, registerVipRequest(zoneUuid))
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition), "%v", err)
			Expect(err).To(MatchError(ContainSubstring("exhausted")))

			_, err = admin.VIP.DeleteVIP(ctx, &svccore.UuidRequest{UUID: first.GetUUID()})
			Expect(err).NotTo(HaveOccurred())
			third := registerVip(ctx, admin, zoneUuid)
			Expect(third.GetIp()).To(Equal(first.GetIp()))
		}, NodeTimeout(time.Minute))

		It("takes the ip of a vip in the zone of its instance", func(ctx SpecContext) {
			zoneUuid := createRangeZone(ctx, admin, "10.22.0.1", "10.22.0.20")
			outOfZone := createVipWithIp(ctx, admin, "192.168.50.1")
			taken := createVipWithIp(ctx, admin, "10.22.0.10")
			sameIp := createVipWithIp(ctx, admin, "10.22.0.10")

			_, err := admin.Instance.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{
				Instance: env.NewInstance(env.Unique("inst"), zoneUuid, outOfZone),
			})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition), "%v", err)
			Expect(err).To(MatchError(ContainSubstring("outside pool ranges")))

			_, err = admin.Instance.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{
				Instance: env.NewInstance(env.Unique("inst"), zoneUuid, taken),
			})
			Expect(err).NotTo(HaveOccurred())
			_, err = admin.Instance.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{
				Instance: env.NewInstance(env.Unique("inst"), zoneUuid, sameIp),
			})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition), "%v", err)
			Expect(err).To(MatchError(ContainSubstring("already allocated")))
		}, NodeTimeout(time.Minute))

		It("keeps a zone holding an ip until the ip is released", func(ctx SpecContext) {
			zoneUuid := createRangeZone(ctx, admin, "10.23.0.1", "10.23.0.2")
			registered := registerVip(ctx, admin, zoneUuid)

			_, err := admin.Zone.DeleteZone(ctx, &svccore.UuidRequest{UUID: zoneUuid})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition), "%v", err)

			_, err = admin.VIP.DeleteVIP(ctx, &svccore.UuidRequest{UUID: registered.GetUUID()})
			Expect(err).NotTo(HaveOccurred())
			_, err = admin.Zone.DeleteZone(ctx, &svccore.UuidRequest{UUID: zoneUuid})
			Expect(err).NotTo(HaveOccurred())
		}, NodeTimeout(time.Minute))
	})

	// the remote ipam listens on the host, controller reaches it through the testcontainers host access
	Describe("remote", Ordered, func() {
		var (
			g      *env.Controller
			admin  *env.Api
			remote *fakeRemoteIpam
		)

		BeforeAll(func(ctx SpecContext) {
			remote = newFakeRemoteIpam()
			lis, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).NotTo(HaveOccurred())
			server := &http.Server{Handler: remote}
			go func() { _ = server.Serve(lis) }()
			DeferCleanup(server.Close)
			remotePort := lis.Addr().(*net.TCPAddr).Port

			g = startController(ctx, env.ControllerOptions{
				Name:      "controller-ipam-remote",
				Store:     env.StoreSqlite,
				HostPorts: []int{remotePort},
				Ipam: map[string]any{"remote": map[string]any{
					"url":     fmt.Sprintf("http://host.testcontainers.internal:%d", remotePort),
					"headers": map[string]any{"Authorization": remoteIpamToken},
				}},
			})
			admin = loginAdmin(ctx, g)
		}, NodeTimeout(10*time.Minute))

		BeforeEach(func() {
			registerControllerForDump(g)
		})

		It("sends the zone to the remote when it is created", func(ctx SpecContext) {
			zoneUuid := createRangeZone(ctx, admin, "10.30.0.1", "10.30.0.3")

			Expect(remote.syncedZone(zoneUuid)).To(HaveField("Pools", ConsistOf(HaveField("Name", "default"))))
			Expect(remote.unauthorizedCalls()).To(BeZero())
		}, NodeTimeout(time.Minute))

		It("registers vips with the ips of the remote and releases them on delete", func(ctx SpecContext) {
			zoneUuid := createRangeZone(ctx, admin, "10.31.0.1", "10.31.0.2")

			first := registerVip(ctx, admin, zoneUuid)
			second := registerVip(ctx, admin, zoneUuid)
			Expect([]string{first.GetIp(), second.GetIp()}).To(ConsistOf("10.31.0.1", "10.31.0.2"))
			Expect(remote.owners(zoneUuid)).To(Equal(map[string]string{
				first.GetIp(): first.GetUUID(), second.GetIp(): second.GetUUID(),
			}))

			_, err := admin.VIP.RegisterVip(ctx, registerVipRequest(zoneUuid))
			Expect(err).To(MatchError(ContainSubstring(errRemoteExhausted)))

			_, err = admin.VIP.DeleteVIP(ctx, &svccore.UuidRequest{UUID: first.GetUUID()})
			Expect(err).NotTo(HaveOccurred())
			Expect(remote.owners(zoneUuid)).To(Equal(map[string]string{second.GetIp(): second.GetUUID()}))
			third := registerVip(ctx, admin, zoneUuid)
			Expect(third.GetIp()).To(Equal(first.GetIp()))
			Expect(remote.unauthorizedCalls()).To(BeZero())
		}, NodeTimeout(time.Minute))

		It("acquires the ip of an instance vip and releases it with the instance", func(ctx SpecContext) {
			zoneUuid := createRangeZone(ctx, admin, "10.32.0.1", "10.32.0.20")
			vipUuid := createVipWithIp(ctx, admin, "10.32.0.10")
			sameIp := createVipWithIp(ctx, admin, "10.32.0.10")

			instResp, err := admin.Instance.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{
				Instance: env.NewInstance(env.Unique("inst"), zoneUuid, vipUuid),
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(remote.owners(zoneUuid)).To(Equal(map[string]string{"10.32.0.10": vipUuid}))

			// the remote refuses an ip of another owner with a 409, the controller gives its message back
			_, err = admin.Instance.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{
				Instance: env.NewInstance(env.Unique("inst"), zoneUuid, sameIp),
			})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition), "%v", err)
			Expect(err).To(MatchError(ContainSubstring(errRemoteTaken)))

			_, err = admin.Instance.DeleteInstance(ctx, &svccore.UuidRequest{UUID: instResp.GetInstance().GetUUID()})
			Expect(err).NotTo(HaveOccurred())
			Expect(remote.owners(zoneUuid)).To(BeEmpty())
			Expect(remote.unauthorizedCalls()).To(BeZero())
		}, NodeTimeout(time.Minute))
	})
})

func createRangeZone(ctx context.Context, admin *env.Api, ipMin, ipMax string) string {
	GinkgoHelper()
	resp, err := admin.Zone.CreateZone(ctx, &svczone.CreateZoneRequest{Zone: &zone.Zone{
		Name:  env.Unique("zone"),
		Pools: []*zone.IpPool{{Name: "default", IpRanges: []*zone.IpRange{{IpMin: ipMin, IpMax: ipMax}}}},
	}})
	Expect(err).NotTo(HaveOccurred())
	return resp.GetZone().GetUUID()
}

func registerVipRequest(zoneUuid string) *svcvip.RegisterVipRequest {
	return &svcvip.RegisterVipRequest{
		Name:     env.Unique("vip"),
		Location: &svcvip.RegisterVipRequest_ZoneUuid{ZoneUuid: zoneUuid},
	}
}

func registerVip(ctx context.Context, admin *env.Api, zoneUuid string) *vip.VIP {
	GinkgoHelper()
	resp, err := admin.VIP.RegisterVip(ctx, registerVipRequest(zoneUuid))
	Expect(err).NotTo(HaveOccurred())
	return resp.GetVip()
}

func createVipWithIp(ctx context.Context, admin *env.Api, ip string) string {
	GinkgoHelper()
	resp, err := admin.VIP.CreateVIP(ctx, &svcvip.CreateVIPRequest{Vip: env.NewVip(env.Unique("vip"), ip)})
	Expect(err).NotTo(HaveOccurred())
	return resp.GetVip().GetUUID()
}

const (
	errRemoteExhausted = "fake ipam: pool exhausted"
	errRemoteTaken     = "fake ipam: ip taken by another owner"
)

// fakeRemoteIpam answers the REST contract of controller/ipams/remote.go, it allocates in the first ip range
// of a pool and keeps the zones it is sent
type fakeRemoteIpam struct {
	mutex        *sync.Mutex
	zones        map[string]*zone.Zone
	ownerOfIp    map[string]map[string]string // zone uuid → ip → owner
	unauthorized int
}

type fakeRemoteIpamRequest struct {
	ZoneUuid string          `json:"zone_uuid"`
	Zone     json.RawMessage `json:"zone"`
	Pool     string          `json:"pool"`
	Owner    string          `json:"owner"`
	Ip       string          `json:"ip"`
}

func newFakeRemoteIpam() *fakeRemoteIpam {
	return &fakeRemoteIpam{
		mutex:     &sync.Mutex{},
		zones:     make(map[string]*zone.Zone),
		ownerOfIp: make(map[string]map[string]string),
	}
}

func (f *fakeRemoteIpam) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if req.Header.Get("Authorization") != remoteIpamToken {
		f.unauthorized++
		writeRemoteIpam(w, http.StatusUnauthorized, map[string]any{"error": "fake ipam: no token"})
		return
	}
	var body fakeRemoteIpamRequest
	err := json.NewDecoder(req.Body).Decode(&body)
	if err != nil {
		writeRemoteIpam(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	zoneInfo := &zone.Zone{}
	if len(body.Zone) > 0 {
		err = protojson.Unmarshal(body.Zone, zoneInfo)
		if err != nil {
			writeRemoteIpam(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	if f.ownerOfIp[body.ZoneUuid] == nil {
		f.ownerOfIp[body.ZoneUuid] = make(map[string]string)
	}
	owners := f.ownerOfIp[body.ZoneUuid]

	switch req.URL.Path {
	case "/v1/ipam/sync-zone":
		f.zones[body.ZoneUuid] = zoneInfo
		writeRemoteIpam(w, http.StatusOK, nil)
	case "/v1/ipam/acquire":
		ip, found := freeIpOfPool(zoneInfo, body.Pool, owners)
		if !found {
			writeRemoteIpam(w, http.StatusConflict, map[string]any{"error": errRemoteExhausted})
			return
		}
		owners[ip] = body.Owner
		writeRemoteIpam(w, http.StatusOK, map[string]any{"ip": ip})
	case "/v1/ipam/acquire-specific":
		owner, taken := owners[body.Ip]
		if taken && owner != body.Owner {
			writeRemoteIpam(w, http.StatusConflict, map[string]any{"error": errRemoteTaken})
			return
		}
		owners[body.Ip] = body.Owner
		writeRemoteIpam(w, http.StatusOK, nil)
	case "/v1/ipam/release":
		_, allocated := owners[body.Ip]
		if !allocated {
			writeRemoteIpam(w, http.StatusNotFound, map[string]any{"error": "fake ipam: not allocated"})
			return
		}
		delete(owners, body.Ip)
		writeRemoteIpam(w, http.StatusOK, nil)
	case "/v1/ipam/release-owner":
		for _, zoneOwners := range f.ownerOfIp {
			maps.DeleteFunc(zoneOwners, func(_, owner string) bool { return owner == body.Owner })
		}
		writeRemoteIpam(w, http.StatusOK, nil)
	case "/v1/ipam/has-allocations":
		writeRemoteIpam(w, http.StatusOK, map[string]any{"has_allocations": len(owners) > 0})
	default:
		writeRemoteIpam(w, http.StatusNotFound, map[string]any{"error": "fake ipam: unknown path " + req.URL.Path})
	}
}

func freeIpOfPool(zoneInfo *zone.Zone, poolName string, owners map[string]string) (string, bool) {
	poolIndex := slices.IndexFunc(zoneInfo.GetPools(), func(p *zone.IpPool) bool { return p.GetName() == poolName })
	if poolIndex < 0 || len(zoneInfo.GetPools()[poolIndex].GetIpRanges()) == 0 {
		return "", false
	}
	ipRange := zoneInfo.GetPools()[poolIndex].GetIpRanges()[0]
	ipMax := netip.MustParseAddr(ipRange.GetIpMax())
	for ip := netip.MustParseAddr(ipRange.GetIpMin()); ip.Compare(ipMax) <= 0; ip = ip.Next() {
		_, taken := owners[ip.String()]
		if !taken {
			return ip.String(), true
		}
	}
	return "", false
}

func writeRemoteIpam(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (f *fakeRemoteIpam) syncedZone(zoneUuid string) *zone.Zone {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.zones[zoneUuid]
}

func (f *fakeRemoteIpam) owners(zoneUuid string) map[string]string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return maps.Clone(f.ownerOfIp[zoneUuid])
}

func (f *fakeRemoteIpam) unauthorizedCalls() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.unauthorized
}
