package as3

import (
	"fmt"
	"slices"
	"strings"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/types/known/anypb"
)

// SchemaVersion is the lowest AS3 schema holding every property the agent declares.
const SchemaVersion = "3.50.0"

type Input struct {
	InstanceUUID   string
	Tenant         string
	VIPs           []*vip.VIP
	VirtualServers map[string]*vs.VirtualServer
	Backends       map[string]*backend.Backend
	Certificates   map[string]*cert.Certificate
	// Endpoints are the endpoints the agent discovered, by backend UUID, for the backends AgentDiscovers. A backend not
	// discovered yet has no entry.
	Endpoints map[string][]*core.IPAddr
}

type Result struct {
	Tenant Tenant
	// Warnings are the approximations made for each resource, sorted.
	Warnings map[ResourceKey][]string
	// Errors are the resources left out of the tenant because they can't be declared.
	Errors map[ResourceKey]string
	// Missing lists, for a resource, the resources it references that are not known yet (or a backend not discovered
	// yet). Its parts depending on them are left out of the tenant.
	Missing map[ResourceKey][]ResourceKey
	// Pools are the pools built from each backend, for its endpoint stats.
	Pools []PoolRef
}

type PoolRef struct {
	BackendUUID string
	Tenant      string
	App         string
	Pool        string
}

// TenantRemark marks the tenants an instance owns: the agent only ever deletes tenants carrying its own remark.
func TenantRemark(instanceUUID string) string {
	return "flock instance " + instanceUUID
}

type renderer struct {
	in  Input
	res Result
}

type appRender struct {
	vip  *vip.VIP
	name string
	app  *Application
}

// portKey is what two services of a VIP can't share: tcp, http and tls virtual servers all listen on tcp
type portKey struct {
	port uint32
	udp  bool
}

func Render(in Input) Result {
	r := &renderer{
		in: in,
		res: Result{
			Tenant:   Tenant{Remark: TenantRemark(in.InstanceUUID), Apps: map[string]*Application{}},
			Warnings: map[ResourceKey][]string{},
			Errors:   map[ResourceKey]string{},
			Missing:  map[ResourceKey][]ResourceKey{},
		},
	}
	vips := slices.Clone(in.VIPs)
	slices.SortFunc(vips, func(a, b *vip.VIP) int { return strings.Compare(a.GetUUID(), b.GetUUID()) })
	for _, v := range vips {
		r.renderVIP(v)
	}
	slices.SortFunc(r.res.Pools, func(a, b PoolRef) int {
		return strings.Compare(a.App+"/"+a.Pool, b.App+"/"+b.Pool)
	})
	return r.res
}

func (r *renderer) renderVIP(v *vip.VIP) {
	a := &appRender{
		vip:  v,
		name: AppName(v.GetUUID()),
		app:  &Application{Remark: remark(v.GetName()), Objects: map[string]any{}},
	}
	portUsers := map[portKey]*vs.VirtualServer{}
	for _, vsUUID := range slices.Sorted(slices.Values(v.GetVirtualServersUuid())) {
		virtualServer, ok := r.in.VirtualServers[vsUUID]
		if !ok {
			r.res.missing(v, ResourceKey{TypeUrl: helpers.TypeUrl[*vs.VirtualServer](), UUID: vsUUID})
			continue
		}
		key := portKey{port: virtualServer.GetPort(), udp: virtualServer.GetUdpRouteConfig() != nil}
		if user, taken := portUsers[key]; taken {
			r.res.fail(virtualServer, fmt.Sprintf("port %d of the VIP is already used by virtual server %s, the F5 agent can't share it",
				key.port, user.GetName()))
			continue
		}
		portUsers[key] = virtualServer
		r.renderVirtualServer(a, virtualServer)
	}
	r.res.Tenant.Apps[a.name] = a.app
}

func (r *renderer) renderVirtualServer(a *appRender, virtualServer *vs.VirtualServer) {
	switch {
	case virtualServer.GetTcpRouteConfig() != nil:
		r.renderTCP(a, virtualServer)
	case virtualServer.GetUdpRouteConfig() != nil:
		r.renderUDP(a, virtualServer)
	case virtualServer.GetHttpRoutesConfig() != nil:
		r.renderHTTP(a, virtualServer)
	case virtualServer.GetTlsTcpRoutesConfig() != nil:
		r.renderTLS(a, virtualServer)
	}
}

func (r *renderer) renderTCP(a *appRender, virtualServer *vs.VirtualServer) {
	config := virtualServer.GetTcpRouteConfig()
	use, ok := r.useBackend(a, virtualServer, config.GetBackendUuid(), false)
	if !ok {
		return
	}
	svc := newService(a, virtualServer, classServiceTCP)
	svc.Pool = use.pool
	use.applyTo(svc)
	if config.GetAcceptProxyProtocol() {
		r.addProxyRecv(a, virtualServer, svc)
	}
	r.finishService(a, virtualServer, svc, config.GetTypedOptions())
}

func (r *renderer) renderUDP(a *appRender, virtualServer *vs.VirtualServer) {
	config := virtualServer.GetUdpRouteConfig()
	use, ok := r.useBackend(a, virtualServer, config.GetBackendUuid(), true)
	if !ok {
		return
	}
	svc := newService(a, virtualServer, classServiceUDP)
	svc.Pool = use.pool
	use.applyTo(svc)
	r.finishService(a, virtualServer, svc, config.GetTypedOptions())
}

// finishService applies what every kind of virtual server shares, its limits and its F5 options last so they can
// replace what the agent generated, and adds the service to the application.
func (r *renderer) finishService(a *appRender, virtualServer *vs.VirtualServer, svc *Service, typedOptions *anypb.Any) {
	if virtualServer.GetEnableL3Log() {
		r.res.warn(virtualServer, "enable_l3_log is not supported by the F5 agent, it is ignored")
	}
	r.applyLimits(virtualServer, svc)
	r.applyVirtualServerOptions(virtualServer, svc, typedOptions)
	a.app.Objects[ServiceName(virtualServer.GetUUID())] = svc
}

func newService(a *appRender, virtualServer *vs.VirtualServer, class string) *Service {
	return &Service{
		Class:              class,
		Remark:             remark(virtualServer.GetName()),
		VirtualAddresses:   []string{a.vip.GetIp()},
		VirtualPort:        virtualServer.GetPort(),
		Snat:               snatAuto,
		PersistenceMethods: []any{},
	}
}

// Document is the body of the AS3 request declaring one tenant. AS3 leaves the tenants absent from it untouched.
func Document(instanceUUID, tenantName string, t Tenant, dryRun bool) ([]byte, error) {
	action := actionDeploy
	if dryRun {
		action = actionDryRun
	}
	return marshal(map[string]any{
		"class":   classAS3,
		"action":  action,
		"persist": true,
		"declaration": map[string]any{
			"class":         classADC,
			"schemaVersion": SchemaVersion,
			"id":            "flock-" + instanceUUID,
			tenantName:      t,
		},
	})
}
