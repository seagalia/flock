package cplters

import (
	"context"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli/internal/fakeconnect"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
	"google.golang.org/protobuf/proto"
)

// fakeServer serves read calls from in-memory resources indexed by UUID.
type fakeServer struct {
	backends map[string]*backend.Backend
	routes   map[string]*route.VirtualHost
	vss      map[string]*vs.VirtualServer
	vips     map[string]*vip.VIP
	zones    map[string]*zone.Zone
}

func newFakeServer() *fakeServer {
	return &fakeServer{
		backends: map[string]*backend.Backend{},
		routes:   map[string]*route.VirtualHost{},
		vss:      map[string]*vs.VirtualServer{},
		vips:     map[string]*vip.VIP{},
		zones:    map[string]*zone.Zone{},
	}
}

func lookup[T proto.Message](resources map[string]T, req proto.Message) (T, error) {
	uuid := req.(*svccore.UuidRequest).GetUUID()
	res, ok := resources[uuid]
	if !ok {
		var zero T
		return zero, connect.Errorf(connect.CodeNotFound, "%s not found", uuid)
	}
	return res, nil
}

func (b *fakeServer) client() *clients.Client {
	conn := fakeconnect.New().
		Handle(svcbackendconnect.BackendReadBackendProcedure, func(req proto.Message) (proto.Message, error) {
			res, err := lookup(b.backends, req)
			return &svcbackend.BackendResponse{Backend: res}, err
		}).
		Handle(svcvsconnect.VirtualServerReadVSProcedure, func(req proto.Message) (proto.Message, error) {
			res, err := lookup(b.vss, req)
			return &svcvs.VSResponse{Vs: res}, err
		}).
		Handle(svcvipconnect.VIPReadVIPProcedure, func(req proto.Message) (proto.Message, error) {
			res, err := lookup(b.vips, req)
			return &svcvip.VIPResponse{Vip: res}, err
		}).
		Handle(svczoneconnect.ZoneReadZoneProcedure, func(req proto.Message) (proto.Message, error) {
			res, err := lookup(b.zones, req)
			return &svczone.ZoneResponse{Zone: res}, err
		})
	return clients.New(conn.Client())
}

func tcpVirtualServer(uuid, name, backendUuid string) *vs.VirtualServer { //nolint:unparam // the call sites show the value
	return &vs.VirtualServer{UUID: uuid, Name: name, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
		TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: backendUuid},
	}}
}

var _ = Describe("Completers", func() {
	var server *fakeServer

	BeforeEach(func() {
		server = newFakeServer()
	})

	Describe("ProtoToMap", func() {
		It("uses proto names and omits unpopulated fields", func() {
			data, err := ProtoToMap(&backend.Backend{UUID: "c1", Name: "backend"})

			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(Equal(map[string]any{"UUID": "c1", "name": "backend"}))
		})
	})

	Describe("VirtualServerComplete", func() {
		It("replaces the tcp route backend uuid by the backend", func() {
			server.backends["c1"] = &backend.Backend{UUID: "c1", Name: "tcp-backend"}
			completer := &VirtualServerComplete{VS: tcpVirtualServer("vs1", "tcp", "c1")}

			Expect(completer.Complete(context.Background(), server.client())).To(Succeed())
			Expect(completer.TcpRoute.Backend.GetName()).To(Equal("tcp-backend"))
			Expect(completer.TcpRoute.Route.GetBackendUuid()).To(BeEmpty())

			data, err := completer.ToMap()
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveKeyWithValue("tcp_route_config",
				HaveKeyWithValue("backend", HaveKeyWithValue("name", "tcp-backend"))))
		})

		It("replaces the udp route backend uuid by the backend", func() {
			server.backends["c1"] = &backend.Backend{UUID: "c1", Name: "dns-backend"}
			completer := &VirtualServerComplete{VS: &vs.VirtualServer{UUID: "vs1", RouteConfig: &vs.VirtualServer_UdpRouteConfig{
				UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: "c1"},
			}}}

			Expect(completer.Complete(context.Background(), server.client())).To(Succeed())

			data, err := completer.ToMap()
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(HaveKeyWithValue("udp_route_config",
				HaveKeyWithValue("backend", HaveKeyWithValue("name", "dns-backend"))))
		})

		It("keeps the backend of each tls route in order", func() {
			server.backends["c1"] = &backend.Backend{UUID: "c1", Name: "first"}
			server.backends["c2"] = &backend.Backend{UUID: "c2", Name: "second"}
			completer := &VirtualServerComplete{VS: &vs.VirtualServer{UUID: "vs1", RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{
				TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: []*vs.TlsTcpRouteConfig{
					{BackendUuid: "c1"},
					{BackendUuid: "c2"},
				}},
			}}}

			Expect(completer.Complete(context.Background(), server.client())).To(Succeed())
			Expect(completer.TlsTcpRoute).To(HaveLen(2))
			Expect(completer.TlsTcpRoute[0].Backend.GetName()).To(Equal("first"))
			Expect(completer.TlsTcpRoute[1].Backend.GetName()).To(Equal("second"))
			Expect(completer.TlsTcpRoute[0].Route.GetBackendUuid()).To(BeEmpty())

			data, err := completer.ToMap()
			Expect(err).NotTo(HaveOccurred())
			Expect(data["tls_tcp_routes_config"]).To(HaveLen(2))
		})

		It("returns the error when a backend cannot be read", func() {
			completer := &VirtualServerComplete{VS: tcpVirtualServer("vs1", "tcp", "missing")}

			err := completer.Complete(context.Background(), server.client())

			Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
		})
	})

	Describe("VIPComplete", func() {
		It("replaces virtual server uuids by completed virtual servers", func() {
			server.backends["c1"] = &backend.Backend{UUID: "c1", Name: "backend"}
			server.vss["vs1"] = tcpVirtualServer("vs1", "tcp", "c1")
			vipRes := &vip.VIP{UUID: "vip1", Name: "public", VirtualServersUuid: []string{"vs1"}}
			completer := &VIPComplete{VIP: vipRes}

			Expect(completer.Complete(context.Background(), server.client())).To(Succeed())
			Expect(vipRes.GetVirtualServersUuid()).To(BeNil())

			data, err := completer.ToMap()
			Expect(err).NotTo(HaveOccurred())
			vss := data["virtual_servers"].([]map[string]any)
			Expect(vss).To(HaveLen(1))
			Expect(vss[0]).To(HaveKeyWithValue("name", "tcp"))
		})

		It("names the virtual server that failed", func() {
			server.vss["vs1"] = tcpVirtualServer("vs1", "tcp", "missing")
			completer := &VIPComplete{VIP: &vip.VIP{VirtualServersUuid: []string{"vs1"}}}

			err := completer.Complete(context.Background(), server.client())

			Expect(err).To(MatchError(ContainSubstring("failed to complete virtual server vs1")))
		})
	})

	Describe("InstanceComplete", func() {
		It("resolves the zone and replaces vip uuids by vips", func() {
			server.zones["z1"] = &zone.Zone{Name: "production"}
			server.vips["vip1"] = &vip.VIP{UUID: "vip1", Name: "public"}
			inst := &instance.Instance{UUID: "i1", Name: "lb", ZoneUuid: "z1", VipsUuid: []string{"vip1"}}
			completer := &InstanceComplete{Instance: inst}

			Expect(completer.Complete(context.Background(), server.client())).To(Succeed())
			Expect(completer.Zone.GetName()).To(Equal("production"))
			Expect(inst.GetVipsUuid()).To(BeNil())

			data, err := completer.ToMap()
			Expect(err).NotTo(HaveOccurred())
			vips := data["vips"].([]map[string]any)
			Expect(vips).To(HaveLen(1))
			Expect(vips[0]).To(HaveKeyWithValue("name", "public"))
		})

		It("returns the error when the zone cannot be read", func() {
			completer := &InstanceComplete{Instance: &instance.Instance{UUID: "i1", ZoneUuid: "missing"}}

			err := completer.Complete(context.Background(), server.client())

			Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
		})
	})
})
