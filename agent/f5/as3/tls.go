package as3

import (
	"slices"
	"strings"

	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
)

// renderTLS declares a TLS over TCP virtual server: one Service_TCP and one iRule reading the SNI of the ClientHello
// before the BIG-IP touches the stream. The client SSL profile starts disabled, the iRule enables it for the routes
// terminating TLS and leaves it off for the passthrough ones, so one virtual server serves both like envoy does.
func (r *renderer) renderTLS(a *appRender, virtualServer *vs.VirtualServer) {
	config := virtualServer.GetTlsTcpRoutesConfig()
	var terminations []*core.TLS
	for _, rt := range config.GetRoutes() {
		if rt.GetTls() != nil {
			terminations = append(terminations, rt.GetTls())
		}
	}

	svc := newService(a, virtualServer, classServiceTCP)
	terminates := len(terminations) > 0
	if terminates {
		serverTLS, ok := r.renderServerTLS(a, virtualServer, terminations)
		if !ok {
			return
		}
		svc.ServerTLS = serverTLS
	}

	routing := &routing{r: r, a: a, virtualServer: virtualServer, anyTLSBackend: r.anyTLSTermination(config.GetRoutes())}
	rule := routing.sniIRule(config, terminates)
	routing.applyBackends(svc)
	a.app.Objects[IRuleName(iRulePurposeSNI, virtualServer.GetUUID())] = &IRule{Class: classIRule, IRule: rule}
	svc.IRules = append(svc.IRules, IRuleName(iRulePurposeSNI, virtualServer.GetUUID()))
	if config.GetAcceptProxyProtocol() {
		// the SNI iRule strips the header itself, it must be gone before the ClientHello is read
		r.res.warn(virtualServer, proxyProtocolWarning)
	}
	r.finishService(a, virtualServer, svc, config.GetTypedOptions())
}

// anyTLSTermination tells a terminating route reaches a backend connecting with TLS: the service then gets a server
// SSL profile. Passthrough routes never use one.
func (r *renderer) anyTLSTermination(routes []*vs.TlsTcpRouteConfig) bool {
	for _, rt := range routes {
		if rt.GetTls() != nil && r.in.Backends[rt.GetBackendUuid()].GetEnableTls() {
			return true
		}
	}
	return false
}

func (h *routing) sniIRule(config *vs.TlsTcpRoutesConfig, terminates bool) string {
	routes := config.GetRoutes()
	serverNames := make([][]string, len(routes))
	for i, rt := range routes {
		serverNames[i] = slices.Concat(rt.GetTls().GetServerNames(), rt.GetPassthrough().GetServerNames())
	}
	branches, catchAll := nameBranches("$flock_sni", serverNames)
	block := func(i int) string {
		passthrough := routes[i].GetPassthrough() != nil
		command, ok := h.toBackend(routes[i].GetBackendUuid(), passthrough)
		if !ok {
			return "reject\n"
		}
		if !passthrough {
			command = "SSL::enable\n" + command
		}
		return command
	}
	fallback := func() string {
		if catchAll < 0 {
			return "reject\n"
		}
		return block(catchAll)
	}

	var rule strings.Builder
	rule.WriteString("when CLIENT_ACCEPTED {\n")
	if terminates {
		rule.WriteString("    SSL::disable\n")
	}
	rule.WriteString("    TCP::collect\n}\n")
	rule.WriteString("when CLIENT_DATA {\n")
	if config.GetAcceptProxyProtocol() {
		rule.WriteString(proxyRecvBody)
	}
	rule.WriteString(readSNI)
	rule.WriteString(indent(nameSwitch(branches, block, fallback), "    "))
	rule.WriteString("    TCP::release\n}\n")
	return rule.String()
}

// readSNI waits for the whole first TLS record and sets flock_sni to the lowercased server name of the ClientHello,
// "" when the client sends none or doesn't speak TLS. The offsets follow RFC 8446: record header (5), handshake
// header (4), version (2), random (32), then the session id, cipher suites and compression methods, each with its
// length, before the extensions. binary scan reads signed integers, hence the masks.
const readSNI = `    if { [TCP::payload length] < 5 } {
        TCP::collect
        return
    }
    binary scan [TCP::payload 5] cSS flock_tls_type flock_tls_version flock_tls_len
    set flock_tls_len [expr {$flock_tls_len & 0xffff}]
    set flock_sni ""
    if { $flock_tls_type == 22 } {
        if { [TCP::payload length] < [expr {$flock_tls_len + 5}] } {
            TCP::collect
            return
        }
        set flock_hello [TCP::payload [expr {$flock_tls_len + 5}]]
        set flock_off 43
        binary scan $flock_hello @${flock_off}c flock_len
        set flock_off [expr {$flock_off + 1 + ($flock_len & 0xff)}]
        binary scan $flock_hello @${flock_off}S flock_len
        set flock_off [expr {$flock_off + 2 + ($flock_len & 0xffff)}]
        binary scan $flock_hello @${flock_off}c flock_len
        set flock_off [expr {$flock_off + 1 + ($flock_len & 0xff)}]
        binary scan $flock_hello @${flock_off}S flock_ext_len
        set flock_off [expr {$flock_off + 2}]
        set flock_ext_end [expr {$flock_off + ($flock_ext_len & 0xffff)}]
        while { $flock_off + 4 <= $flock_ext_end } {
            binary scan $flock_hello @${flock_off}SS flock_ext_type flock_ext_size
            set flock_ext_size [expr {$flock_ext_size & 0xffff}]
            if { ($flock_ext_type & 0xffff) == 0 } {
                binary scan $flock_hello @[expr {$flock_off + 7}]S flock_name_len
                set flock_name_start [expr {$flock_off + 9}]
                set flock_name_end [expr {$flock_name_start + ($flock_name_len & 0xffff) - 1}]
                set flock_sni [string tolower [string range $flock_hello $flock_name_start $flock_name_end]]
                break
            }
            set flock_off [expr {$flock_off + 4 + $flock_ext_size}]
        }
    }
`
