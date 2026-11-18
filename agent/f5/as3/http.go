package as3

import (
	"cmp"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"

	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

const (
	proxyProtocolWarning = "the proxy protocol header is read and removed, F5 keeps the address of the sending proxy as client address"
	noRoute              = `HTTP::respond 404 content "no route"` + "\n"
)

// renderHTTP declares an http virtual server. Routing is one generated iRule: LTM policies can't answer a 404 when no
// route matches, nor a direct response, nor a regex path, and an iRule keeps flock's first match semantics exactly.
func (r *renderer) renderHTTP(a *appRender, virtualServer *vs.VirtualServer) {
	config := virtualServer.GetHttpRoutesConfig()
	usedExtAuths := route.ExternalAuths(config.GetExternalAuths()).Used(config.GetVirtualHosts())
	if !r.knowsExtAuthBackends(virtualServer, usedExtAuths) {
		return
	}
	hasTLS := slices.ContainsFunc(config.GetVirtualHosts(), func(vh *route.VirtualHost) bool { return len(vh.GetTlsOptions()) > 0 })
	svc := newService(a, virtualServer, classServiceHTTP)
	if hasTLS {
		serverTLS, ok := r.renderServerTLS(a, virtualServer, tlsOptionsOfVirtualHosts(config.GetVirtualHosts()))
		if !ok {
			return
		}
		svc.Class = classServiceHTTPS
		svc.Redirect80 = new(bool)
		svc.ServerTLS = serverTLS
		for _, vh := range config.GetVirtualHosts() {
			if len(vh.GetTlsOptions()) == 0 {
				r.res.warn(virtualServer, fmt.Sprintf("virtual host %s has no TLS options, it is served over TLS with the certificates of the other virtual hosts",
					strings.Join(vh.GetDomains(), ", ")))
			}
		}
	}

	// after the TLS step, so a virtual server left out for its certificates leaves no auth object behind
	extAuthRules := r.renderExtAuths(a, virtualServer, usedExtAuths)
	scheme := "http"
	if hasTLS {
		scheme = "https"
	}
	routing := &routing{r: r, a: a, virtualServer: virtualServer, scheme: scheme,
		anyTLSBackend: r.anyTLSBackend(config.GetVirtualHosts()), extAuthRules: extAuthRules}
	rule := routing.httpIRule(config.GetVirtualHosts())
	routing.applyBackends(svc)

	if config.GetAcceptProxyProtocol() {
		r.addProxyRecv(a, virtualServer, svc)
	}
	a.app.Objects[IRuleName(iRulePurposeHTTP, virtualServer.GetUUID())] = &IRule{Class: classIRule, IRule: rule}
	svc.IRules = append(svc.IRules, IRuleName(iRulePurposeHTTP, virtualServer.GetUUID()))
	r.finishService(a, virtualServer, svc, config.GetTypedOptions())
}

func (r *renderer) addProxyRecv(a *appRender, virtualServer *vs.VirtualServer, svc *Service) {
	name := IRuleName(iRulePurposeProxyRecv, virtualServer.GetUUID())
	a.app.Objects[name] = &IRule{Class: classIRule, IRule: proxyRecvIRule}
	svc.IRules = append(svc.IRules, name)
	r.res.warn(virtualServer, proxyProtocolWarning)
}

func (r *renderer) anyTLSBackend(vhosts []*route.VirtualHost) bool {
	for _, vh := range vhosts {
		for _, rt := range vh.GetRoutes() {
			if r.in.Backends[rt.GetBackendUuid()].GetEnableTls() {
				return true
			}
		}
	}
	return false
}

func tlsOptionsOfVirtualHosts(vhosts []*route.VirtualHost) []*core.TLS {
	var options []*core.TLS
	for _, vh := range vhosts {
		options = append(options, vh.GetTlsOptions()...)
	}
	return options
}

// renderServerTLS declares the certificates of the TLS options and the client SSL profile serving them on their SNI.
// It is false when the virtual server can't terminate TLS yet (no certificate known) or at all (none usable).
func (r *renderer) renderServerTLS(a *appRender, virtualServer *vs.VirtualServer, options []*core.TLS) (string, bool) {
	var entries []TLSServerCert
	waiting := false
	for _, option := range options {
		for _, certUUID := range option.GetCertificatesUuid() {
			c, ok := r.in.Certificates[certUUID]
			if !ok {
				r.res.missing(virtualServer, ResourceKey{TypeUrl: helpers.TypeUrl[*cert.Certificate](), UUID: certUUID})
				waiting = true
				continue
			}
			if c.GetCertChain() == "" || c.GetPrivateKey() == "" {
				r.res.warn(virtualServer, fmt.Sprintf("certificate %s has no chain or no key, it is left out", c.GetName()))
				continue
			}
			a.app.Objects[CertName(certUUID)] = &Certificate{
				Class: classCertificate, Remark: remark(c.GetName()),
				Certificate: c.GetCertChain(), PrivateKey: c.GetPrivateKey(), ChainCA: c.GetCa(),
			}
			if len(option.GetServerNames()) == 0 {
				entries = append(entries, TLSServerCert{Certificate: CertName(certUUID)})
			}
			for _, serverName := range option.GetServerNames() {
				entries = append(entries, TLSServerCert{Certificate: CertName(certUUID), MatchToSNI: serverName})
			}
		}
	}
	if len(entries) == 0 {
		if !waiting {
			r.res.fail(virtualServer, "no usable certificate to terminate TLS")
		}
		return "", false
	}
	// the BIG-IP takes the first profile as the default one of the SNI, so the entries without server name go first
	slices.SortFunc(entries, func(x, y TLSServerCert) int {
		return cmp.Or(cmp.Compare(x.MatchToSNI, y.MatchToSNI), cmp.Compare(x.Certificate, y.Certificate))
	})
	name := ServerTLSName(virtualServer.GetUUID())
	a.app.Objects[name] = &TLSServer{Class: classTLSServer, Certificates: slices.Compact(entries)}
	return name, true
}

type routing struct {
	r             *renderer
	a             *appRender
	virtualServer *vs.VirtualServer
	scheme        string // of an http virtual server
	uses          []backendUse
	// anyTLSBackend tells the service gets a server SSL profile, the routes to plain backends must then disable it
	anyTLSBackend bool
	extAuthRules  map[string]string // iRule path of each used external auth policy, by name
}

func (h *routing) policies() route.ExternalAuths {
	return h.virtualServer.GetHttpRoutesConfig().GetExternalAuths()
}

func (h *routing) httpIRule(vhosts []*route.VirtualHost) string {
	domains := make([][]string, len(vhosts))
	for i, vh := range vhosts {
		domains[i] = vh.GetDomains()
	}
	branches, catchAll := nameBranches("$flock_host", domains)
	fallback := func() string {
		if catchAll < 0 {
			return h.routes(nil)
		}
		return h.routes(vhosts[catchAll])
	}

	var rule strings.Builder
	rule.WriteString("when HTTP_REQUEST {\n")
	rule.WriteString("    set flock_host [string tolower [getfield [HTTP::host] \":\" 1]]\n")
	rule.WriteString("    set flock_path [HTTP::path]\n")
	rule.WriteString("    set flock_lpath [string tolower $flock_path]\n")
	// removed on every route, checked or not, so no backend gets a value the client made up
	for _, header := range h.policies().StrippedHeaders() {
		rule.WriteString("    HTTP::header remove " + tclQuote(header) + "\n")
	}
	rule.WriteString(indent(nameSwitch(branches, func(i int) string { return h.routes(vhosts[i]) }, fallback), "    "))
	rule.WriteString("}\n")
	return rule.String()
}

type nameBranch struct {
	condition string
	target    int // index of the virtual host or route the branch selects
	order     int // 0 exact, 1 suffix wildcard, 2 prefix wildcard
	length    int
}

// nameBranches orders the names of each target like envoy: exact names first in their order, then wildcards longest
// first. catchAll is the first target with "*" or no name at all, -1 when there is none.
func nameBranches(variable string, names [][]string) (branches []nameBranch, catchAll int) {
	catchAll = -1
	for target, targetNames := range names {
		if len(targetNames) == 0 {
			targetNames = []string{"*"}
		}
		var exact []string
		for _, name := range targetNames {
			name = strings.ToLower(name)
			switch {
			case name == "*":
				if catchAll < 0 {
					catchAll = target
				}
			case strings.HasPrefix(name, "*"):
				branches = append(branches, nameBranch{condition: nameGlob(variable, name), target: target, order: 1, length: len(name)})
			case strings.HasSuffix(name, "*"):
				branches = append(branches, nameBranch{condition: nameGlob(variable, name), target: target, order: 2, length: len(name)})
			default:
				exact = append(exact, variable+" eq "+tclQuote(name))
			}
		}
		if len(exact) > 0 {
			branches = append(branches, nameBranch{condition: strings.Join(exact, " || "), target: target})
		}
	}
	slices.SortStableFunc(branches, func(x, y nameBranch) int {
		return cmp.Or(cmp.Compare(x.order, y.order), cmp.Compare(y.length, x.length))
	})
	return branches, catchAll
}

// nameSwitch is the if/elseif chain running the block of the first matching branch, fallback when none matches.
// Blocks are built in the order of the chain, fallback last.
func nameSwitch(branches []nameBranch, block func(target int) string, fallback func() string) string {
	if len(branches) == 0 {
		return fallback()
	}
	var chain strings.Builder
	for i, b := range branches {
		keyword := "} elseif"
		if i == 0 {
			keyword = "if"
		}
		fmt.Fprintf(&chain, "%s { %s } {\n", keyword, b.condition)
		chain.WriteString(indent(block(b.target), "    "))
	}
	chain.WriteString("} else {\n")
	chain.WriteString(indent(fallback(), "    "))
	chain.WriteString("}\n")
	return chain.String()
}

func nameGlob(variable, name string) string {
	return "[string match " + tclQuote(globEscaper.Replace(name)) + " " + variable + "]"
}

// routes is the first match chain of the routes of a virtual host, a 404 when nothing matches (vh may be nil).
func (h *routing) routes(vh *route.VirtualHost) string {
	if vh == nil || len(vh.GetRoutes()) == 0 {
		return noRoute
	}
	var block strings.Builder
	for i, rt := range vh.GetRoutes() {
		keyword := "} elseif"
		if i == 0 {
			keyword = "if"
		}
		fmt.Fprintf(&block, "%s { %s } {\n", keyword, pathCondition(vh.GetPathPrefix(), rt.GetMatch()))
		policy := h.policies().Of(vh, rt)
		if policy != nil {
			fmt.Fprintf(&block, "    if { ![call %s::check] } { return }\n", h.extAuthRules[policy.GetName()])
		}
		block.WriteString(indent(h.action(rt), "    "))
	}
	block.WriteString("} else {\n")
	block.WriteString(indent(noRoute, "    "))
	block.WriteString("}\n")
	return block.String()
}

// pathCondition matches like the starling agent: the path prefix of the virtual host joined to prefixes and paths,
// its root route on whole path segments, regexes on the whole path.
func pathCondition(pathPrefix string, match *route.RouteMatch) string {
	caseSensitive := match.GetCaseSensitive() == nil || match.GetCaseSensitive().GetValue()
	variable, normalize := "$flock_path", func(s string) string { return s }
	if !caseSensitive {
		variable, normalize = "$flock_lpath", strings.ToLower
	}
	switch spec := match.GetPathSpecifier().(type) {
	case *route.RouteMatch_Path:
		return variable + " eq " + tclQuote(normalize(joinPathPrefix(pathPrefix, spec.Path)))
	case *route.RouteMatch_SafeRegex:
		flags := "--"
		if !caseSensitive {
			flags = "-nocase --"
		}
		return "[regexp " + flags + " " + tclQuote("^(?:"+spec.SafeRegex+")$") + " $flock_path]"
	default:
		vhostPrefix := normalize(strings.TrimSuffix(pathPrefix, "/"))
		isRouteOnPrefix := vhostPrefix != "" && (match.GetPrefix() == "/" || match.GetPrefix() == "")
		// the prefix "/api" alone would also match "/apiv2": the root route of a path prefix stops at a segment
		if isRouteOnPrefix {
			return variable + " eq " + tclQuote(vhostPrefix) + " || " + variable + " starts_with " + tclQuote(vhostPrefix+"/")
		}
		full := normalize(joinPathPrefix(pathPrefix, match.GetPrefix()))
		if full == "" || full == "/" {
			return "1"
		}
		return variable + " starts_with " + tclQuote(full)
	}
}

// joinPathPrefix puts the path prefix of a virtual host before the path of one of its routes, as the starling agent
// does: the route "/" is the prefix itself.
func joinPathPrefix(pathPrefix, path string) string {
	if pathPrefix == "" {
		return path
	}
	if path == "/" || path == "" {
		return pathPrefix
	}
	return strings.TrimSuffix(pathPrefix, "/") + path
}

func (h *routing) action(rt *route.Route) string {
	switch action := rt.GetAction().(type) {
	case *route.Route_Redirect:
		return "HTTP::respond 301 Location " + h.location(action.Redirect) + "\n"
	case *route.Route_DirectResponse:
		return directResponse(action.DirectResponse)
	default:
		command, ok := h.toBackend(rt.GetBackendUuid(), false)
		if !ok {
			return `HTTP::respond 503 content "no backend"` + "\n"
		}
		return command
	}
}

// toBackend is the iRule command sending the traffic to the pool of a backend. It is false when the backend is not
// known yet. A passthrough route forwards the client TLS as is, so the TLS of its backend is left out.
func (h *routing) toBackend(backendUUID string, passthrough bool) (string, bool) {
	use, ok := h.r.useBackend(h.a, h.virtualServer, backendUUID, false)
	if !ok {
		return "", false
	}
	if passthrough && use.clientTLS != nil {
		h.r.res.warn(h.virtualServer, fmt.Sprintf("backend %s connects with TLS, a passthrough route forwards the client TLS as is",
			h.r.in.Backends[backendUUID].GetName()))
		use.clientTLS = nil
	}
	h.uses = append(h.uses, use)
	command := "pool " + h.r.objectPath(h.a, use.pool) + "\n"
	if !h.anyTLSBackend {
		return command, true
	}
	// a server SSL profile on the virtual server applies to every pool, plain endpoints opt out of it, and the choice
	// lasts for the client connection, so every route sets it for the next request on a keep-alive connection
	if use.clientTLS == nil {
		return "SSL::disable serverside\n" + command, true
	}
	return "SSL::enable serverside\n" + command, true
}

// location builds the Location of a redirect, keeping from the request what the redirect doesn't replace.
func (h *routing) location(redirect *route.RedirectAction) string {
	scheme := h.scheme
	if redirect.GetHttpsRedirect() {
		scheme = "https"
	}
	if redirect.GetSchemeRedirect() != "" {
		scheme = redirect.GetSchemeRedirect()
	}
	schemeChanged := scheme != h.scheme

	host := "[HTTP::host]"
	if redirect.GetPortRedirect() != 0 || schemeChanged {
		host = `[getfield [HTTP::host] ":" 1]`
	}
	if redirect.GetHostRedirect() != "" {
		host = tclEscape(redirect.GetHostRedirect())
	}
	port := ""
	isDefaultPort := (scheme == "https" && redirect.GetPortRedirect() == 443) || (scheme == "http" && redirect.GetPortRedirect() == 80)
	if redirect.GetPortRedirect() != 0 && !isDefaultPort {
		port = fmt.Sprintf(":%d", redirect.GetPortRedirect())
	}
	path := "[HTTP::uri]"
	if redirect.GetPathRedirect() != "" {
		path = tclEscape(redirect.GetPathRedirect())
		if !strings.Contains(redirect.GetPathRedirect(), "?") {
			path += `[expr {[HTTP::query] ne "" ? "?[HTTP::query]" : ""}]`
		}
	}
	return `"` + tclEscape(scheme) + "://" + host + port + path + `"`
}

func directResponse(response *route.DirectResponseAction) string {
	if response.GetInlineBytes() != nil {
		encoded := base64.StdEncoding.EncodeToString(response.GetInlineBytes())
		return fmt.Sprintf("HTTP::respond %d content [b64decode %s]\n", response.GetStatus(), tclQuote(encoded))
	}
	return fmt.Sprintf("HTTP::respond %d content %s\n", response.GetStatus(), tclQuote(response.GetInlineString()))
}

// applyBackends carries on the service what the backends of the routes need. A virtual server has one server SSL
// profile: the one of the first backend connecting with TLS is used.
func (h *routing) applyBackends(svc *Service) {
	for _, use := range h.uses {
		if use.clientTLS != nil && svc.ClientTLS == nil {
			svc.ClientTLS = use.clientTLS
		} else if use.clientTLS != nil && svc.ClientTLS != use.clientTLS {
			h.r.res.warn(h.virtualServer, fmt.Sprintf("a virtual server has one server TLS profile, %s is used for every backend connecting with TLS", svc.ClientTLS))
		}
		for _, rule := range use.iRules {
			if !slices.Contains(svc.IRules, rule) {
				svc.IRules = append(svc.IRules, rule)
			}
		}
		if len(use.persistence) > 0 {
			svc.PersistenceMethods = use.persistence
		}
	}
}
