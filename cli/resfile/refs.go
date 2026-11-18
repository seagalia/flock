package resfile

import (
	"strings"

	routev1 "github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	vipv1 "github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	vsv1 "github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
)

// RefPrefix marks a value that names a resource instead of giving its uuid.
const RefPrefix = "name:"

// KindCertificate is the kind of the resources certificates_uuid points to. Files cannot define certificates, so it
// only ever resolves against the store.
const KindCertificate = "certificate"

// Ref is a `name:` value found in a document. Set writes the resolved uuid back into the message.
type Ref struct {
	Kind string
	Name string
	Set  func(uuid string)
}

// RefName tells whether value is a `name:<name>` reference and returns the name.
func RefName(value string) (name string, isRef bool) {
	name, isRef = strings.CutPrefix(value, RefPrefix)
	if !isRef {
		return "", false
	}
	return name, true
}

// Refs lists every `name:` value of the uuid fields of a document, in document order.
func Refs(doc Document) []Ref {
	var found refCollector
	switch msg := doc.Message.(type) {
	case *vipv1.VIP:
		found.uuids(KindVs, msg.VirtualServersUuid)
	case *vsv1.VirtualServer:
		found.virtualServer(msg)
	}
	return found.refs
}

type refCollector struct {
	refs []Ref
}

// uuids records the references among a list of uuids, a reference is replaced in place by its uuid.
func (c *refCollector) uuids(kind string, values []string) {
	for i := range values {
		c.uuid(kind, &values[i])
	}
}

func (c *refCollector) uuid(kind string, value *string) {
	name, isRef := RefName(*value)
	if !isRef {
		return
	}
	c.refs = append(c.refs, Ref{Kind: kind, Name: name, Set: func(uuid string) { *value = uuid }})
}

func (c *refCollector) virtualServer(vs *vsv1.VirtualServer) {
	switch config := vs.RouteConfig.(type) {
	case *vsv1.VirtualServer_TcpRouteConfig:
		c.uuid(KindBackend, &config.TcpRouteConfig.BackendUuid)
	case *vsv1.VirtualServer_UdpRouteConfig:
		c.uuid(KindBackend, &config.UdpRouteConfig.BackendUuid)
	case *vsv1.VirtualServer_TlsTcpRoutesConfig:
		for _, route := range config.TlsTcpRoutesConfig.GetRoutes() {
			c.uuid(KindBackend, &route.BackendUuid)
			c.uuids(KindCertificate, route.GetTls().GetCertificatesUuid())
		}
	case *vsv1.VirtualServer_HttpRoutesConfig:
		for _, policy := range config.HttpRoutesConfig.GetExternalAuths() {
			c.uuid(KindBackend, &policy.BackendUuid)
		}
		for _, host := range config.HttpRoutesConfig.GetVirtualHosts() {
			for _, route := range host.GetRoutes() {
				c.httpRoute(route)
			}
			for _, tls := range host.GetTlsOptions() {
				c.uuids(KindCertificate, tls.GetCertificatesUuid())
			}
		}
	}
}

func (c *refCollector) httpRoute(route *routev1.Route) {
	backend, isBackendAction := route.Action.(*routev1.Route_BackendUuid)
	if !isBackendAction {
		return
	}
	c.uuid(KindBackend, &backend.BackendUuid)
}
