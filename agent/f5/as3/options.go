package as3

import (
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	f5_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/f5_options/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

// applyVirtualServerOptions attaches the existing BIG-IP objects the F5 options of a virtual server name. They replace
// what the agent generated (a TLS profile, a persistence), the iRules come after the generated ones. AS3 refuses a
// property the class of the service doesn't have, so an option the service can't carry is left out with a warning.
func (r *renderer) applyVirtualServerOptions(virtualServer *vs.VirtualServer, svc *Service, packed *anypb.Any) {
	opts := unpackOptions[*f5_vsopts.VirtualServerOptions](&r.res, virtualServer, packed)
	isHTTP := svc.Class == classServiceHTTP || svc.Class == classServiceHTTPS
	isUDP := svc.Class == classServiceUDP
	ignore := func(option, applies string) {
		r.res.warn(virtualServer, option+" only applies to "+applies+" virtual servers, it is ignored")
	}

	for _, path := range opts.GetIrules() {
		svc.IRules = append(svc.IRules, BigipRef{Bigip: path})
	}
	if opts.GetPersistenceProfile() != "" {
		svc.PersistenceMethods = []any{BigipRef{Bigip: opts.GetPersistenceProfile()}}
	}
	if opts.GetFallbackPersistenceProfile() != "" {
		svc.FallbackPersistenceMethod = BigipRef{Bigip: opts.GetFallbackPersistenceProfile()}
	}
	setRef := func(option, path string, applies bool, appliesTo string, target *any) {
		if path == "" {
			return
		}
		if !applies {
			ignore(option, appliesTo)
			return
		}
		*target = BigipRef{Bigip: path}
	}
	setRef(optionWafPolicy, opts.GetWafPolicy(), isHTTP, "http", &svc.PolicyWAF)
	setRef(optionHTTPProfile, opts.GetHttpProfile(), isHTTP, "http", &svc.ProfileHTTP)
	setRef(optionClientTLSProfile, opts.GetClientTlsProfile(), !isUDP, "tcp, tls and http", &svc.ServerTLS)
	setRef(optionServerTLSProfile, opts.GetServerTlsProfile(), !isUDP, "tcp, tls and http", &svc.ClientTLS)
	setRef(optionTCPProfile, opts.GetTcpProfile(), !isUDP, "tcp, tls and http", &svc.ProfileTCP)
	setRef(optionUDPProfile, opts.GetUdpProfile(), isUDP, "udp", &svc.ProfileUDP)

	switch snat := opts.GetSnat().(type) {
	case *f5_vsopts.VirtualServerOptions_None:
		if snat.None {
			svc.Snat = snatNone
		}
	case *f5_vsopts.VirtualServerOptions_Pool:
		svc.Snat = BigipRef{Bigip: snat.Pool}
	}
}
