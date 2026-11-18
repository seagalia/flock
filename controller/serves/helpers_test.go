package serves_test

import (
	"context"
	"net/netip"
	"path/filepath"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/controller/dbstore"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/controller/ipams"
	"github.com/seagalia/flock/controller/lockers"
	"github.com/seagalia/flock/controller/permissioners"
	. "github.com/seagalia/flock/controller/serves"
	"github.com/seagalia/flock/sdk/contexes"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	corev1 "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/flock/services/discovery/v1/svcdiscoveryconnect"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
	"github.com/seagalia/flock/sdk/observe"
	"github.com/seagalia/flock/sdk/rpcserver"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	adminUser  = "admin"
	adminGroup = "admins"
)

// dbMan and dbStoreMan are shared by all specs because they register prometheus collectors globally
var (
	dbMan      *dbs.DbManager
	dbStoreMan *dbstore.DbStoreManager
	dbIpam     *ipams.Db
	dbLeases   *lockers.Leases
)

var _ = BeforeSuite(func() {
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	dsn := filepath.Join(GinkgoT().TempDir(), "test.db")
	var err error
	dbMan, err = dbs.NewDbManager(ctx, dbs.DbTypeSqlite, dsn, time.Hour, time.Hour, 1, 1)
	Expect(err).ToNot(HaveOccurred())
	dbStoreMan, err = dbstore.NewDbStoreManager(ctx, dbMan, "secret")
	Expect(err).ToNot(HaveOccurred())
	dbIpam, err = ipams.NewDb(context.Background(), dbMan)
	Expect(err).ToNot(HaveOccurred())
	dbLeases, err = lockers.NewLeases(ctx, dbMan)
	Expect(err).ToNot(HaveOccurred())
})

var _ = BeforeEach(func() {
	tables := []string{
		"ForeignResources", "Permissions", "Metadata",
		"Backend", "Instance", "VIP", "VirtualServer", "Certificate", "Secret", "Zone", "AgentNodes",
		ipams.TableIPAMRecycled, ipams.TableIPAMAllocation, ipams.TableIPAMRange, ipams.TableIPAMPool, lockers.TableLeases,
	}
	for _, table := range tables {
		_, err := dbMan.Exec(context.Background(), dbs.MakeDbQuery("DELETE FROM "+table, nil))
		Expect(err).ToNot(HaveOccurred())
	}
})

type testServer struct {
	*FlockServer
	perm permissioners.Permissioner
}

func newTestServer(instantiater instantiaters.Instantiater) *testServer {
	return newTestServerWithIpam(instantiater, nil)
}

func newTestServerWithIpam(instantiater instantiaters.Instantiater, ipam ipams.Ipam) *testServer {
	DeferCleanup(observe.OffAll)
	perm := permissioners.NewMetrics(permissioners.NewDbStorePerm(dbStoreMan))
	admConf := config.AdminConfig{Users: []string{adminUser}, Groups: []string{adminGroup}}
	server := NewFlockServer(context.Background(), nil, admConf, nil, dbStoreMan, nil, nil, perm, instantiater, ipam, lockers.NewMemory(), dbLeases)
	// a spread still running would write in the store of the next spec
	DeferCleanup(server.WaitBackground)
	return &testServer{FlockServer: server, perm: perm}
}

// createZone gives the uuid of a new zone with the subnet 10.0.0.0/24
func (t *testServer) createZone(name string) string {
	GinkgoHelper()
	resp, err := t.CreateZone(userCtx(adminUser), &svczone.CreateZoneRequest{Zone: newZone(name)})
	Expect(err).ToNot(HaveOccurred())
	return resp.GetZone().GetUUID()
}

// allocatedIps gives the ips the ipam holds in the zone, with their owner
func allocatedIps(zoneUuid string) map[string]string {
	GinkgoHelper()
	rows, err := dbMan.Query(context.Background(), dbs.MakeDbQuery(
		"SELECT ip, owner FROM "+ipams.TableIPAMAllocation+" WHERE pool_id = ?", []any{zoneUuid},
	))
	Expect(err).ToNot(HaveOccurred())
	defer rows.Close()
	allocated := make(map[string]string)
	for rows.Next() {
		var ip []byte
		var owner string
		Expect(rows.Scan(&ip, &owner)).To(Succeed())
		addr, ok := netip.AddrFromSlice(ip)
		Expect(ok).To(BeTrue())
		allocated[addr.String()] = owner
	}
	return allocated
}

func storedRangeCount(zoneUuid string) int {
	GinkgoHelper()
	rows, err := dbMan.Query(context.Background(), dbs.MakeDbQuery(
		"SELECT COUNT(*) FROM "+ipams.TableIPAMRange+" WHERE pool_id = ?", []any{zoneUuid},
	))
	Expect(err).ToNot(HaveOccurred())
	defer rows.Close()
	Expect(rows.Next()).To(BeTrue())
	var count int
	Expect(rows.Scan(&count)).To(Succeed())
	return count
}

func userCtx(username string, groups ...string) context.Context {
	return contexes.WithUserInfo(context.Background(), username, groups)
}

// throughChain gives a client calling the server through the connect chain of the controller (defaults,
// validation), as a real call does. The in-process transport keeps the identity the test context carries.
func (t *testServer) throughChain() *connect.Client {
	connectServer := rpcserver.NewServer(func(ctx context.Context, _ string) (context.Context, error) {
		return ctx, nil
	})
	svcbackendconnect.RegisterBackendHandler(connectServer, t.FlockServer)
	svcinstanceconnect.RegisterInstanceHandler(connectServer, t.FlockServer)
	svcvipconnect.RegisterVIPHandler(connectServer, t.FlockServer)
	svcvsconnect.RegisterVirtualServerHandler(connectServer, t.FlockServer)
	svczoneconnect.RegisterZoneHandler(connectServer, t.FlockServer)
	return connect.NewClient(connectinprocess.New(connectServer))
}

func expectCode(err error, code connect.Code) {
	GinkgoHelper()
	Expect(err).To(HaveOccurred())
	Expect(connect.CodeOf(err)).To(Equal(code), "error was: %v", err)
}

func newZone(name string) *zone.Zone {
	return &zone.Zone{
		Name:  name,
		Zone:  "z1",
		Pools: []*zone.IpPool{{Name: "default", Subnets: []*zone.Subnet{{Ip: "10.0.0.0", PrefixLength: 24}}}},
	}
}

func newBackend(name string) *backend.Backend {
	return &backend.Backend{
		Name: name,
		HealthCheck: &corev1.HealthCheck{
			Timeout:       durationpb.New(time.Second),
			Interval:      durationpb.New(5 * time.Second),
			HealthChecker: &corev1.HealthCheck_NoHealthCheck{NoHealthCheck: &corev1.NoHealthCheck{}},
		},
		Endpoint: &backend.Backend_IpAddrGroup{IpAddrGroup: &corev1.IPAddrGroup{
			IpAddresses: []*corev1.IPAddr{{Ip: "10.0.0.10", Port: 8080}},
		}},
	}
}

func newVs(name string, port uint32, backendUuid string) *vs.VirtualServer {
	return &vs.VirtualServer{
		Name: name,
		Port: port,
		RouteConfig: &vs.VirtualServer_TcpRouteConfig{TcpRouteConfig: &vs.TcpRouteConfig{
			BackendUuid: backendUuid,
		}},
	}
}

func newHttpVs(name string, port uint32, vhosts ...*route.VirtualHost) *vs.VirtualServer {
	return &vs.VirtualServer{
		Name: name,
		Port: port,
		RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{
			VirtualHosts: vhosts,
		}},
	}
}

func newVip(name string, virtualServersUuid ...string) *vip.VIP {
	return &vip.VIP{
		Name:               name,
		Ip:                 "10.0.0.1",
		VirtualServersUuid: virtualServersUuid,
	}
}

func newVirtualHost(backendUuid string) *route.VirtualHost {
	return &route.VirtualHost{
		Domains: []string{"*"},
		Routes: []*route.Route{{
			Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
			Action: &route.Route_BackendUuid{BackendUuid: backendUuid},
		}},
	}
}

func newInstance(name, zoneUuid string, vipsUuid ...string) *instance.Instance {
	return &instance.Instance{
		Name:         name,
		ZoneUuid:     zoneUuid,
		ResourceType: "small",
		VipsUuid:     vipsUuid,
	}
}

// withHeaders gives ctx as served by connect for a call with the request headers of ctx and kv (key, value pairs)
func withHeaders(ctx context.Context, kv ...string) context.Context {
	info := &connect.CallInfo{}
	if current, ok := connect.CallInfoForServerContext(ctx); ok {
		for key, values := range current.RequestHeader().All() {
			info.RequestHeader().SetValues(key, values)
		}
	}
	for i := 0; i+1 < len(kv); i += 2 {
		info.RequestHeader().Add(kv[i], kv[i+1])
	}
	return rpctest.ServerContextWithInfo(ctx, info)
}

// withAsUser makes request act as another user, only admins are allowed to do so
func withAsUser(ctx context.Context, asUserJson string) context.Context {
	return withHeaders(ctx, AsUserMetaKey, asUserJson)
}

type adsServerMock = MockDiscoveryStream

func newAdsServerMock(ctrl *gomock.Controller) *adsServerMock {
	return NewMockDiscoveryStream(ctrl)
}

// agentContext gives the server context of a call over mTLS from 127.0.0.1 with a client certificate named commonName.
// The HTTP call it was captured from has ended, so its cancellation is dropped.
func agentContext(commonName string) context.Context {
	GinkgoHelper()
	serverTLS, clientTLS := rpctest.NewTLS(commonName)
	ctx, err := rpctest.ServerContextOverHTTP(serverTLS, clientTLS, nil)
	Expect(err).ToNot(HaveOccurred())
	return context.WithoutCancel(ctx)
}

// withPeerAddr gives the server context of the same call as ctx coming from addr
func withPeerAddr(ctx context.Context, addr string) context.Context {
	callInfo, _ := connect.CallInfoForServerContext(ctx)
	return rpctest.ServerContextWithInfo(ctx, &connect.CallInfo{PeerAddr: addr, TransportInfo: callInfo.TransportInfo})
}

// serveDiscovery serves a discovery call reading and writing its messages on stream, through a connect server
// as a real call would be
func serveDiscovery(ctx context.Context, server *FlockServer, stream DiscoveryStream) error {
	_, err := serveDiscoveryCall(ctx, server, stream)
	return err
}

// serveDiscoveryCall is serveDiscovery for the peer of the call ctx was served for, giving the CallInfo of the
// discovery call
func serveDiscoveryCall(ctx context.Context, server *FlockServer, stream DiscoveryStream) (*connect.CallInfo, error) {
	callInfo := &connect.CallInfo{}
	if outer, ok := connect.CallInfoForServerContext(ctx); ok {
		callInfo.PeerAddr = outer.PeerAddr
		callInfo.TransportInfo = outer.TransportInfo
	}
	connectServer := connect.NewServer()
	svcdiscoveryconnect.RegisterAggregatedDiscoveryHandler(connectServer, server)
	err := connectServer.Call(ctx, svcdiscoveryconnect.AggregatedDiscoveryDeltaAggregatedResourcesProcedure, callInfo,
		&discoveryServerStream{stream: stream})
	return callInfo, err
}

// discoveryServerStream is the connect stream of a DiscoveryStream
type discoveryServerStream struct {
	stream DiscoveryStream
}

func (s *discoveryServerStream) Receive(msg any) error {
	req, err := s.stream.Receive()
	if err != nil {
		return err
	}
	proto.Merge(msg.(proto.Message), req)
	return nil
}

func (s *discoveryServerStream) SendHeaders() error {
	return nil
}

func (s *discoveryServerStream) Send(msg any) error {
	return s.stream.Send(msg.(*svcdiscovery.DeltaDiscoveryResponse))
}
