package route

import (
	"slices"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	defaultExternalAuthTimeout       = time.Second
	defaultExternalAuthStatusOnError = 503
)

func (x *ExternalAuth) ProvDefault() {
	if x == nil {
		return
	}
	x.RequestHeaders = lowerAll(x.GetRequestHeaders())
	x.UpstreamHeaders = lowerAll(x.GetUpstreamHeaders())
	x.ClientHeaders = lowerAll(x.GetClientHeaders())
	if x.GetTimeout() == nil {
		x.Timeout = durationpb.New(defaultExternalAuthTimeout)
	}
	if x.GetStatusOnError() == 0 {
		x.StatusOnError = defaultExternalAuthStatusOnError
	}
}

func lowerAll(names []string) []string {
	if names == nil {
		return nil
	}
	lowered := make([]string, len(names))
	for i, name := range names {
		lowered[i] = strings.ToLower(name)
	}
	return lowered
}

// ExternalAuths are the policies of a virtual server (external_auths of its http routes config). Both drivers resolve
// the policy of a route with them, so they can't disagree on which routes are checked.
type ExternalAuths []*ExternalAuth

// Of gives the policy checking the route, nil when the route has none, opted out or names a policy the list doesn't
// define.
func (p ExternalAuths) Of(vh *VirtualHost, rte *Route) *ExternalAuth {
	if rte.GetExternalAuthDisabled() {
		return nil
	}
	name := vh.GetExternalAuth()
	if rte.GetExternalAuthName() != "" {
		name = rte.GetExternalAuthName()
	}
	for _, policy := range p {
		if policy.GetName() == name {
			return policy
		}
	}
	return nil
}

// Used gives the policies checking at least one route of the virtual hosts a driver renders, in the order of the list.
func (p ExternalAuths) Used(vhosts []*VirtualHost) []*ExternalAuth {
	var used []*ExternalAuth
	for _, policy := range p {
		if p.checksARoute(policy, vhosts) {
			used = append(used, policy)
		}
	}
	return used
}

func (p ExternalAuths) checksARoute(policy *ExternalAuth, vhosts []*VirtualHost) bool {
	for _, vh := range vhosts {
		for _, rte := range vh.GetRoutes() {
			if p.Of(vh, rte) == policy {
				return true
			}
		}
	}
	return false
}

// StrippedHeaders gives the headers removed from the requests of every route of the virtual server, checked or not.
func (p ExternalAuths) StrippedHeaders() []string {
	headers := make([]string, 0)
	for _, policy := range p {
		headers = append(headers, policy.GetUpstreamHeaders()...)
	}
	slices.Sort(headers)
	return slices.Compact(headers)
}
