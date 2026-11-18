package models

import (
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/seagalia/flock/provisioners/config"
	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	starling_commopts "github.com/seagalia/flock/sdk/flock/extensions/common/starling_options/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	// NamePrefix starts the name of every resource the docker provisioner makes, so they never clash with the ones
	// of users or other provisioners
	NamePrefix                = "io.seagalia.flock.docker/"
	DefaultHttpVirtualServer  = NamePrefix + "http"
	DefaultHttpsVirtualServer = NamePrefix + "https"

	defaultPath               = "/"
	wildcardDomain            = "*"
	edsRefreshRate            = 5 * time.Second
	defaultHealthInterval     = 5 * time.Second
	defaultHealthTimeout      = 2 * time.Second
	defaultHealthyThreshold   = 2
	defaultUnhealthyThreshold = 3
	defaultStatusStart        = 200
	defaultStatusEnd          = 400
)

// Converter turns the labels of a container into flock resources.
type Converter struct {
	edsUrl        *core.URL
	edsCa         string
	agentUuid     string
	httpPort      uint32
	httpsPort     uint32
	defaultDomain string
}

// NewConverter reads the docker block of cnf. agentUuid is the uuid the controller gave the provisioner.
func NewConverter(cnf *config.Config, agentUuid string) (*Converter, error) {
	edsUrl, err := core.UrlToCoreUrl(cnf.Docker.Eds.AdvertiseUrl)
	if err != nil {
		return nil, fmt.Errorf("parse docker eds.advertise_url: %w", err)
	}
	var edsCa []byte
	if cnf.TlsConfig != nil && cnf.TlsConfig.CaFile != "" {
		edsCa, err = os.ReadFile(cnf.TlsConfig.CaFile)
		if err != nil {
			return nil, fmt.Errorf("read the eds ca: %w", err)
		}
	}
	return &Converter{
		edsUrl:        edsUrl,
		edsCa:         string(edsCa),
		agentUuid:     agentUuid,
		httpPort:      cnf.Docker.HttpPort,
		httpsPort:     cnf.Docker.HttpsPort,
		defaultDomain: cnf.Docker.DefaultDomain,
	}, nil
}

func (c *Converter) Backend(b *Base) (*backendv1.Backend, error) {
	name := b.Name
	eds := &backendv1.EDS{Url: proto.CloneOf(c.edsUrl), ServiceName: name, Ca: c.edsCa}
	backend := &backendv1.Backend{
		Name:        NamePrefix + name,
		LbPolicy:    b.lbPolicy(),
		Endpoint:    &backendv1.Backend_Eds{Eds: eds},
		HealthCheck: b.healthCheck(),
		RefreshRate: durationpb.New(edsRefreshRate),
		Metadata:    c.metadata(),
	}
	if b.Backend != nil && b.Backend.TLS != nil {
		backend.EnableTls = b.Backend.TLS.Enable
		backend.Sni = b.Backend.TLS.ServerName
	}
	if b.protocol() == ProtocolHttp2 {
		httpProtocolOptions, err := anypb.New(upstreamHttp2())
		if err != nil {
			return nil, fmt.Errorf("pack http protocol options: %w", err)
		}
		backend.HttpProtocolOptions = httpProtocolOptions
	}
	return backend, nil
}

// Certificate answers the certificate the acme provisioner orders for the domains, or nil when the container needs
// none: TLS off or passed through, or a certificate given with io.seagalia.flock.tls.cert, which the caller resolves.
func (c *Converter) Certificate(b *Base) (*cert.Certificate, error) {
	needsAcme := b.terminatesTls() && b.GivenCert() == ""
	if !needsAcme {
		return nil, nil
	}
	name := b.Name
	domains := c.domains(b)
	if slices.Contains(domains, wildcardDomain) {
		return nil, fmt.Errorf(
			"%s: %stls.enable with no %stls.cert can't order a certificate for every domain (*)",
			name, LabelPrefix, LabelPrefix,
		)
	}
	metadata := c.metadata()
	acmeKey, acmeValue := config.MakeProvisionerMetadata(config.ACMEProvisionerName)
	metadata[acmeKey] = acmeValue
	return &cert.Certificate{
		Name:        NamePrefix + name,
		DomainNames: domains,
		Metadata:    metadata,
	}, nil
}

func (c *Converter) VirtualHost(b *Base, backendUuid, certUuid string) (*route.VirtualHost, error) {
	name := b.Name
	if b.isPassthrough() {
		return nil, fmt.Errorf("%s: %stls.passthrough is for tcp only", name, LabelPrefix)
	}
	domains := c.domains(b)
	backendRoute := &route.Route{
		Name:   name,
		Match:  b.routeMatch(),
		Action: &route.Route_BackendUuid{BackendUuid: backendUuid},
	}
	if b.AuthPolicy() != "" {
		backendRoute.ExternalAuth = &route.Route_ExternalAuthName{ExternalAuthName: b.AuthPolicy()}
	}
	virtualHost := &route.VirtualHost{
		Domains: domains,
		Routes:  []*route.Route{backendRoute},
	}
	if certUuid != "" {
		virtualHost.TlsOptions = []*core.TLS{{
			ServerNames:      serverNames(domains),
			CertificatesUuid: []string{certUuid},
		}}
	}
	return virtualHost, nil
}

// RedirectVirtualHost answers the virtual host sending the plain http requests of the container to https, to join
// DefaultHttpVirtualServer, or nil when io.seagalia.flock.tls.redirect is off.
func (c *Converter) RedirectVirtualHost(b *Base) (*route.VirtualHost, error) {
	isRedirected := b.tlsEnabled() && b.TLS.Redirect
	if !isRedirected {
		return nil, nil
	}
	name := b.Name
	httpsRedirect := &route.RedirectAction{
		SchemeRewriteSpecifier: &route.RedirectAction_HttpsRedirect{HttpsRedirect: true},
	}
	return &route.VirtualHost{
		Domains: c.domains(b),
		Routes: []*route.Route{{
			Name:   name,
			Match:  b.routeMatch(),
			Action: &route.Route_Redirect{Redirect: httpsRedirect},
		}},
	}, nil
}

// VirtualServer is the virtual server of a tcp or udp container, which has no host to share a virtual server on.
func (c *Converter) VirtualServer(b *Base, backendUuid string) (*vs.VirtualServer, error) {
	name := b.Name
	if b.ListenPort == nil {
		return nil, fmt.Errorf("%s: io.seagalia.flock.listen_port is required for tcp and udp", name)
	}
	if b.IsTlsTcp() {
		return nil, fmt.Errorf("%s: a tcp container with TLS joins a TLS virtual server, see TlsTcpRoute", name)
	}
	if b.AuthPolicy() != "" {
		return nil, fmt.Errorf("%s: %sauth.policy is for http and http2 only", name, LabelPrefix)
	}
	protocol := b.protocol()
	port := uint32(*b.ListenPort)
	virtualServer := &vs.VirtualServer{
		Name:     fmt.Sprintf("%s%s-%d", NamePrefix, protocol, port),
		Port:     port,
		Metadata: c.metadata(),
	}
	if b.VirtualServer != nil {
		virtualServer.Name = *b.VirtualServer
	}
	switch protocol {
	case ProtocolTCP:
		virtualServer.RouteConfig = &vs.VirtualServer_TcpRouteConfig{
			TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: backendUuid},
		}
	case ProtocolUDP:
		virtualServer.RouteConfig = &vs.VirtualServer_UdpRouteConfig{
			UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: backendUuid},
		}
	default:
		return nil, fmt.Errorf("%s: a %s container joins a virtual server, see JoinedVirtualServerRef", name, protocol)
	}
	return virtualServer, nil
}

// JoinedVirtualServerRef is the uuid or name of the virtual server the virtual host of an http container joins.
func (c *Converter) JoinedVirtualServerRef(b *Base) string {
	if b.VirtualServer != nil {
		return *b.VirtualServer
	}
	if b.tlsEnabled() {
		return DefaultHttpsVirtualServer
	}
	return DefaultHttpVirtualServer
}

// DefaultVirtualServer builds DefaultHttpVirtualServer or DefaultHttpsVirtualServer around its first virtual host,
// for the caller that found none to join. A virtual server named by io.seagalia.flock.virtual_server is never created.
func (c *Converter) DefaultVirtualServer(ref string, virtualHost *route.VirtualHost) (*vs.VirtualServer, error) {
	var port uint32
	switch ref {
	case DefaultHttpVirtualServer:
		port = c.httpPort
	case DefaultHttpsVirtualServer:
		port = c.httpsPort
	default:
		return nil, fmt.Errorf("virtual server %s doesn't exist", ref)
	}
	return &vs.VirtualServer{
		Name: ref,
		Port: port,
		RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{
			VirtualHosts: []*route.VirtualHost{virtualHost},
		}},
		Metadata: c.metadata(),
	}, nil
}

// ExternalAuth answers the policy the container provides with io.seagalia.flock.auth.provide.name, checked by the
// auth service behind backendUuid, or nil when it provides none. The policy is validated here so a bad label fails
// this container alone, instead of the write of every virtual server the policy joins.
func (c *Converter) ExternalAuth(b *Base, backendUuid string) (*route.ExternalAuth, error) {
	isProvider := b.Auth != nil && b.Auth.Provide != nil && b.Auth.Provide.Name != ""
	if !isProvider {
		return nil, nil
	}
	if !b.IsHttp() {
		return nil, fmt.Errorf("%s: %sauth.provide is for http and http2 only", b.Name, LabelPrefix)
	}
	provide := b.Auth.Provide
	policy := &route.ExternalAuth{
		Name:            provide.Name,
		BackendUuid:     backendUuid,
		Path:            provide.Path,
		RequestHeaders:  provide.RequestHeaders,
		UpstreamHeaders: provide.UpstreamHeaders,
		ClientHeaders:   provide.ClientHeaders,
		StatusOnError:   provide.StatusOnError,
	}
	if policy.Path == "" {
		policy.Path = defaultPath
	}
	if provide.Timeout != nil {
		policy.Timeout = durationpb.New(time.Duration(*provide.Timeout))
	}
	if provide.FailureMode != nil {
		policy.FailureMode = route.FailureMode(*provide.FailureMode)
	}
	// with the defaults the controller gives, so the policy compares equal to the stored one and validates
	policy.ProvDefault()
	err := helpers.Validate(policy)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid %sauth.provide labels: %w", b.Name, LabelPrefix, err)
	}
	return policy, nil
}

// JoinExternalAuth answers a copy of stored holding policy. The policy of the same name on the same backend is the
// one of the same container, replaced. The same name on another backend is a policy of a user, never overwritten.
func JoinExternalAuth(stored *vs.VirtualServer, policy *route.ExternalAuth) (*vs.VirtualServer, error) {
	joined := proto.CloneOf(stored)
	httpConfig := joined.GetHttpRoutesConfig()
	if httpConfig == nil {
		return nil, fmt.Errorf("virtual server %s is not http, it can't take an external auth", stored.GetName())
	}
	index := slices.IndexFunc(httpConfig.GetExternalAuths(), func(existing *route.ExternalAuth) bool {
		return existing.GetName() == policy.GetName()
	})
	if index < 0 {
		httpConfig.ExternalAuths = append(httpConfig.ExternalAuths, proto.CloneOf(policy))
		return joined, nil
	}
	if httpConfig.ExternalAuths[index].GetBackendUuid() != policy.GetBackendUuid() {
		return nil, fmt.Errorf(
			"virtual server %s already has an external auth %s the docker provisioner doesn't own",
			stored.GetName(), policy.GetName(),
		)
	}
	httpConfig.ExternalAuths[index] = proto.CloneOf(policy)
	return joined, nil
}

// JoinVirtualHost answers a copy of stored holding the routes of virtualHost. A virtual server refuses two virtual
// hosts sharing a domain, so the routes join the virtual host with the same domains when there is one, replacing
// the routes of the same name: containers on one host with different paths become routes of one virtual host.
func JoinVirtualHost(stored *vs.VirtualServer, virtualHost *route.VirtualHost) (*vs.VirtualServer, error) {
	joined := proto.CloneOf(stored)
	httpConfig := joined.GetHttpRoutesConfig()
	if httpConfig == nil {
		return nil, fmt.Errorf("virtual server %s is not http, it can't take a virtual host", stored.GetName())
	}
	index := slices.IndexFunc(httpConfig.GetVirtualHosts(), func(existing *route.VirtualHost) bool {
		return sameDomains(existing.GetDomains(), virtualHost.GetDomains())
	})
	if index < 0 {
		httpConfig.VirtualHosts = append(httpConfig.VirtualHosts, proto.CloneOf(virtualHost))
		return joined, nil
	}
	existing := httpConfig.VirtualHosts[index]
	for _, newRoute := range virtualHost.GetRoutes() {
		existing.Routes = insertRoute(existing.GetRoutes(), proto.CloneOf(newRoute))
	}
	if len(existing.GetTlsOptions()) == 0 {
		existing.TlsOptions = virtualHost.GetTlsOptions()
	}
	return joined, nil
}

// RemoveRoutes answers a copy of stored without the routes named routeName, dropping the virtual hosts left with no
// route. A virtual server left with no virtual host is invalid: the caller deletes it instead.
func RemoveRoutes(stored *vs.VirtualServer, routeName string) *vs.VirtualServer {
	cleaned := proto.CloneOf(stored)
	httpConfig := cleaned.GetHttpRoutesConfig()
	if httpConfig == nil {
		return cleaned
	}
	isNamed := func(r *route.Route) bool { return r.GetName() == routeName }
	httpConfig.VirtualHosts = slices.DeleteFunc(httpConfig.VirtualHosts, func(virtualHost *route.VirtualHost) bool {
		if !slices.ContainsFunc(virtualHost.GetRoutes(), isNamed) {
			return false
		}
		virtualHost.Routes = slices.DeleteFunc(virtualHost.Routes, isNamed)
		return len(virtualHost.Routes) == 0
	})
	return cleaned
}

func (c *Converter) domains(b *Base) []string {
	if b.Domains != nil && len(*b.Domains) > 0 {
		return *b.Domains
	}
	if c.defaultDomain == "" {
		return []string{b.Service}
	}
	return []string{b.Service + "." + c.defaultDomain}
}

func (c *Converter) metadata() map[string]string {
	dockerKey, dockerValue := config.MakeProvisionerMetadata(config.DockerProvisionerName)
	return map[string]string{
		helpers.MetadataLockKey:     "true",
		helpers.MetadataProvisionBy: c.agentUuid,
		dockerKey:                   dockerValue,
	}
}

func durationOr(d *Duration, fallback time.Duration) time.Duration {
	if d == nil {
		return fallback
	}
	return time.Duration(*d)
}

func thresholdOr(t *Threshold, fallback uint32) uint32 {
	if t == nil {
		return fallback
	}
	return uint32(*t)
}

// serverNames leaves the TLS entry or route of the wildcard domain with no server names, so it gets the connections no
// other one matches.
func serverNames(domains []string) []string {
	if slices.Equal(domains, []string{wildcardDomain}) {
		return nil
	}
	return domains
}

func sameDomains(a, b []string) bool {
	return len(a) == len(b) && !slices.ContainsFunc(a, func(domain string) bool {
		return !slices.Contains(b, domain)
	})
}

// insertRoute replaces the route of the same name and keeps the longest prefixes first, since the first route that
// matches handles the request: /api must come before /.
func insertRoute(routes []*route.Route, newRoute *route.Route) []*route.Route {
	routes = slices.DeleteFunc(routes, func(r *route.Route) bool { return r.GetName() == newRoute.GetName() })
	newPrefixLength := len(newRoute.GetMatch().GetPrefix())
	position := slices.IndexFunc(routes, func(r *route.Route) bool {
		prefix := r.GetMatch().GetPrefix()
		return prefix != "" && len(prefix) < newPrefixLength
	})
	if position < 0 {
		return append(routes, newRoute)
	}
	return slices.Insert(routes, position, newRoute)
}

func upstreamHttp2() *starling_commopts.HttpProtocolOptions {
	return &starling_commopts.HttpProtocolOptions{
		UpstreamProtocolOptions: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_{
			ExplicitHttpConfig: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig{
				ProtocolConfig: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
					Http2ProtocolOptions: &starling_commopts.Http2ProtocolOptions{},
				},
			},
		},
	}
}
