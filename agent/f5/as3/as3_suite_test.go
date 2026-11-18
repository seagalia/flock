package as3_test

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
)

func TestAs3(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AS3 Suite")
}

const (
	vipUUID     = "11111111-1111-1111-1111-111111111111"
	vsUUID      = "22222222-2222-2222-2222-222222222222"
	backendUUID = "33333333-3333-3333-3333-333333333333"
	certUUID    = "44444444-4444-4444-4444-444444444444"
)

func newVIP(uuid, ip string, vsUUIDs ...string) *vip.VIP { //nolint:unparam // the call sites show the value
	return &vip.VIP{UUID: uuid, Name: "vip-" + uuid[:4], Ip: ip, VirtualServersUuid: vsUUIDs}
}

func tcpVS(uuid string, port uint32, backendUUID string) *vs.VirtualServer {
	return &vs.VirtualServer{UUID: uuid, Name: "vs-" + uuid[:4], Port: port, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
		TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: backendUUID},
	}}
}

func udpVS(uuid string, port uint32, backendUUID string) *vs.VirtualServer { //nolint:unparam // the call sites show the value
	return &vs.VirtualServer{UUID: uuid, Name: "vs-" + uuid[:4], Port: port, RouteConfig: &vs.VirtualServer_UdpRouteConfig{
		UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: backendUUID},
	}}
}

func staticBackend(uuid string) *backend.Backend {
	return &backend.Backend{
		UUID: uuid,
		Name: "backend-" + uuid[:4],
		Endpoint: &backend.Backend_IpAddrGroup{IpAddrGroup: &core.IPAddrGroup{IpAddresses: []*core.IPAddr{
			{Ip: "10.0.0.10", Port: 8080},
		}}},
		HealthCheck: &core.HealthCheck{
			Timeout:  durationpb.New(2 * time.Second),
			Interval: durationpb.New(10 * time.Second),
			HealthChecker: &core.HealthCheck_TcpHealthCheck{
				TcpHealthCheck: &core.TcpHealthCheck{},
			},
		},
		RefreshRate: durationpb.New(5 * time.Second),
	}
}

func edsBackend(uuid string) *backend.Backend {
	b := staticBackend(uuid)
	b.Endpoint = &backend.Backend_Eds{Eds: &backend.EDS{ServiceName: "svc"}}
	return b
}

func dnsBackend(uuid, host string, family backend.DNS_LookupFamily) *backend.Backend { //nolint:unparam // the call sites show the value
	b := staticBackend(uuid)
	b.Endpoint = &backend.Backend_Dns{Dns: &backend.DNS{Host: host, Port: 8080, LookupFamily: family}}
	return b
}

func testCert(uuid string) *cert.Certificate {
	return &cert.Certificate{UUID: uuid, Name: "cert-" + uuid[:4], CertChain: "CHAIN", PrivateKey: "KEY"}
}

func input(resources ...proto.Message) as3.Input {
	in := as3.Input{
		InstanceUUID:   "inst-1",
		Tenant:         "flock",
		VirtualServers: map[string]*vs.VirtualServer{},
		Backends:       map[string]*backend.Backend{},
		Endpoints:      map[string][]*core.IPAddr{},
		Certificates:   map[string]*cert.Certificate{},
	}
	for _, res := range resources {
		switch r := res.(type) {
		case *vip.VIP:
			in.VIPs = append(in.VIPs, r)
		case *vs.VirtualServer:
			in.VirtualServers[r.GetUUID()] = r
		case *backend.Backend:
			in.Backends[r.GetUUID()] = r
		case *cert.Certificate:
			in.Certificates[r.GetUUID()] = r
		}
	}
	return in
}

func mustAny(m proto.Message) *anypb.Any {
	a, err := anypb.New(m)
	Expect(err).ToNot(HaveOccurred())
	return a
}

func service(res as3.Result, vipUUID, vsUUID string) *as3.Service { //nolint:unparam // the call sites show the value
	app := res.Tenant.Apps[as3.AppName(vipUUID)]
	ExpectWithOffset(1, app).ToNot(BeNil())
	svc, ok := app.Objects[as3.ServiceName(vsUUID)].(*as3.Service)
	ExpectWithOffset(1, ok).To(BeTrue(), "service %s not found", as3.ServiceName(vsUUID))
	return svc
}

func pool(res as3.Result, vipUUID, backendUUID string) *as3.Pool { //nolint:unparam // the call sites show the value
	app := res.Tenant.Apps[as3.AppName(vipUUID)]
	ExpectWithOffset(1, app).ToNot(BeNil())
	p, ok := app.Objects[as3.PoolName(backendUUID)].(*as3.Pool)
	ExpectWithOffset(1, ok).To(BeTrue(), "pool %s not found", as3.PoolName(backendUUID))
	return p
}

func backendKey(uuid string) as3.ResourceKey {
	return as3.KeyOf(&backend.Backend{UUID: uuid})
}

func vsKey(uuid string) as3.ResourceKey {
	return as3.KeyOf(&vs.VirtualServer{UUID: uuid})
}

func vipKey(uuid string) as3.ResourceKey {
	return as3.KeyOf(&vip.VIP{UUID: uuid})
}
