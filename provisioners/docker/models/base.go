package models

import (
	"fmt"
	"strconv"
	"strings"

	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	ProtocolHTTP  Protocol = "http"
	ProtocolHttp2 Protocol = "http2"
	ProtocolTCP   Protocol = "tcp"
	ProtocolUDP   Protocol = "udp"
)

var protocolTypes = map[string]Protocol{
	"HTTP":  ProtocolHTTP,
	"HTTP2": ProtocolHttp2,
	"TCP":   ProtocolTCP,
	"UDP":   ProtocolUDP,
}

// Base holds the labels of a container, flattened on "." (see Decode).
type Base struct {
	// io.seagalia.flock.enable, the container is ignored unless true
	Enable bool `mapstructure:"enable"`
	// Name has no label, Decode sets it to the compose <project>-<service>
	Name string `mapstructure:"-"`
	// Service has no label, Decode sets it to the compose <service>, the domain of the container by default
	Service string `mapstructure:"-"`
	// io.seagalia.flock.domains, comma-separated hostnames, *.example.com and * allowed, <service>.<docker.default_domain>
	// when unset, or <service> with no default domain
	Domains *Domains `mapstructure:"domains"`
	// io.seagalia.flock.path, path prefix of the route, / when unset
	Path *string `mapstructure:"path"`
	// io.seagalia.flock.protocol, http|http2|tcp|udp, http when unset
	Protocol *Protocol `mapstructure:"protocol"`
	// io.seagalia.flock.docker_network, network to take the IP of the container from
	DockerNetwork *string `mapstructure:"docker_network"`
	// io.seagalia.flock.virtual_server, uuid or name of the virtual server to join, io.seagalia.flock.docker/http or
	// io.seagalia.flock.docker/https when unset
	VirtualServer *string `mapstructure:"virtual_server"`
	// io.seagalia.flock.listen_port, port of the virtual server, required for tcp and udp since they have no host to
	// route on
	ListenPort *Port `mapstructure:"listen_port"`
	// io.seagalia.flock.backend.*
	Backend *Backend `mapstructure:"backend"`
	// io.seagalia.flock.tls.*
	TLS *TLS `mapstructure:"tls"`
	// io.seagalia.flock.lb.*
	LB *LB `mapstructure:"lb"`
	// io.seagalia.flock.health.*
	Health *HealthCheck `mapstructure:"health"`
	// io.seagalia.flock.auth.*
	Auth *Auth `mapstructure:"auth"`
}

// AuthPolicy is the policy named by io.seagalia.flock.auth.policy, empty when the route is not checked
func (b *Base) AuthPolicy() string {
	if b.Auth == nil {
		return ""
	}
	return b.Auth.Policy
}

func (b *Base) protocol() Protocol {
	if b.Protocol == nil {
		return ProtocolHTTP
	}
	return *b.Protocol
}

// IsHttp tells a container joining a virtual server with a virtual host from a tcp or udp one getting its own
func (b *Base) IsHttp() bool {
	protocol := b.protocol()
	return protocol == ProtocolHTTP || protocol == ProtocolHttp2
}

// IsTlsTcp tells a tcp container sharing the TLS virtual server of its port, routed on the SNI, from one getting its
// own virtual server
func (b *Base) IsTlsTcp() bool {
	hasTls := b.tlsEnabled() || b.isPassthrough()
	return b.protocol() == ProtocolTCP && hasTls
}

// HasOwnVirtualServer tells a plain tcp or udp container, alone on its virtual server, from one adding its routes to a
// shared one
func (b *Base) HasOwnVirtualServer() bool {
	return !b.IsHttp() && !b.IsTlsTcp()
}

// GivenCert is the uuid or name of io.seagalia.flock.tls.cert when flock terminates TLS with it, empty otherwise
func (b *Base) GivenCert() string {
	if !b.terminatesTls() {
		return ""
	}
	return b.TLS.Cert
}

func (b *Base) tlsEnabled() bool {
	return b.TLS != nil && b.TLS.Enable
}

func (b *Base) isPassthrough() bool {
	return b.TLS != nil && b.TLS.Passthrough
}

// terminatesTls leaves out passthrough, where the container keeps its own certificate
func (b *Base) terminatesTls() bool {
	return b.tlsEnabled() && !b.isPassthrough()
}

func (b *Base) routeMatch() *route.RouteMatch {
	path := defaultPath
	if b.Path != nil && *b.Path != "" {
		path = *b.Path
	}
	return &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: path}}
}

func (b *Base) lbPolicy() backendv1.Backend_LbPolicy {
	if b.LB == nil || b.LB.Policy == nil {
		return backendv1.Backend_ROUND_ROBIN
	}
	return backendv1.Backend_LbPolicy(*b.LB.Policy)
}

// healthCheck checks the path of io.seagalia.flock.health.path over http, otherwise nothing: the eds answers only the
// containers docker sees healthy.
func (b *Base) healthCheck() *core.HealthCheck {
	health := b.Health
	if health == nil {
		health = &HealthCheck{}
	}
	healthCheck := &core.HealthCheck{
		Interval:           durationpb.New(durationOr(health.Interval, defaultHealthInterval)),
		Timeout:            durationpb.New(durationOr(health.Timeout, defaultHealthTimeout)),
		HealthyThreshold:   thresholdOr(health.HealthyThreshold, defaultHealthyThreshold),
		UnhealthyThreshold: thresholdOr(health.UnhealthyThreshold, defaultUnhealthyThreshold),
		HealthChecker:      &core.HealthCheck_NoHealthCheck{NoHealthCheck: &core.NoHealthCheck{}},
	}
	if health.Path == "" {
		return healthCheck
	}
	status := health.Status
	if status == nil {
		status = &StatusRange{Start: defaultStatusStart, End: defaultStatusEnd}
	}
	healthCheck.HealthChecker = &core.HealthCheck_HttpHealthCheck{HttpHealthCheck: &core.HttpHealthCheck{
		Path:             health.Path,
		ExpectedStatuses: status.toInt64Range(),
	}}
	return healthCheck
}

type Backend struct {
	// io.seagalia.flock.backend.port, port of the container, required unless it exposes exactly one port
	Port *Port `mapstructure:"port"`
	// io.seagalia.flock.backend.tls.*
	TLS *BackendTLS `mapstructure:"tls"`
	// io.seagalia.flock.backend.weight, load balancing weight of the container, used by the weighted lb policies
	Weight *Weight `mapstructure:"weight"`
}

type Protocol string

func (p *Protocol) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}
	protocol, ok := protocolTypes[strings.ToUpper(str)]
	if !ok {
		return fmt.Errorf("invalid protocol: %s", str)
	}
	*p = protocol
	return nil
}

type Port uint16

func (p *Port) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}
	port, err := strconv.ParseUint(str, 10, 16)
	if err != nil {
		return fmt.Errorf("failed to parse port: %w", err)
	}
	*p = Port(port)
	return nil
}

type Domains []string

func (r *Domains) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}
	domains := strings.SplitSeq(str, ",")
	for domain := range domains {
		domain = strings.TrimSpace(domain)
		if domain == "" {
			continue
		}
		*r = append(*r, domain)
	}
	return nil
}

type Threshold uint32

func (t *Threshold) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}
	threshold, err := strconv.ParseUint(str, 10, 32)
	if err != nil {
		return fmt.Errorf("parse threshold: %w", err)
	}
	*t = Threshold(threshold)
	return nil
}

type Weight uint8

func (t *Weight) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}
	weight, err := strconv.ParseUint(str, 10, 8)
	if err != nil {
		return fmt.Errorf("parse weight: %w", err)
	}
	*t = Weight(weight)
	return nil
}
