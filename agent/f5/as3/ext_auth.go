package as3

import (
	"fmt"
	"slices"
	"strings"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

// extAuthDriverHeaders are set by the check itself, a request header of the same name is never copied over them.
var extAuthDriverHeaders = []string{
	"host", "connection", "content-length", "transfer-encoding",
	"x-forwarded-method", "x-forwarded-host", "x-forwarded-uri", "x-forwarded-proto", "x-forwarded-for",
}

// extAuthAnswerHeaders are written by HTTP::respond itself on a deny.
var extAuthAnswerHeaders = []string{"connection", "content-length", "transfer-encoding"}

// httpRespondOptions are the words HTTP::respond reads as options among its header name and value pairs.
var httpRespondOptions = []string{"content", "noserver", "version"}

// knowsExtAuthBackends is false when the backend of a used policy is not known yet, and the virtual server is then
// left out since its checked routes can't be served.
func (r *renderer) knowsExtAuthBackends(virtualServer *vs.VirtualServer, used []*route.ExternalAuth) bool {
	allKnown := true
	for _, policy := range used {
		if _, ok := r.in.Backends[policy.GetBackendUuid()]; !ok {
			r.res.missing(virtualServer, ResourceKey{TypeUrl: helpers.TypeUrl[*backend.Backend](), UUID: policy.GetBackendUuid()})
			allKnown = false
		}
	}
	return allKnown
}

// renderExtAuths declares, for each used policy, the internal virtual the check connects to and the iRule holding its
// proc. It gives the iRule path of each policy by name.
func (r *renderer) renderExtAuths(a *appRender, virtualServer *vs.VirtualServer, used []*route.ExternalAuth) map[string]string {
	paths := make(map[string]string, len(used))
	for _, policy := range used {
		use, _ := r.useBackend(a, virtualServer, policy.GetBackendUuid(), false)
		serviceName := ExtAuthServiceName(virtualServer.GetUUID(), policy.GetName())
		svc := &Service{
			Class:              classServiceTCP,
			Remark:             remark(virtualServer.GetName() + " external auth " + policy.GetName()),
			VirtualType:        virtualTypeInternal,
			Pool:               use.pool,
			Snat:               snatAuto,
			PersistenceMethods: []any{},
		}
		use.applyTo(svc)
		a.app.Objects[serviceName] = svc

		ruleName := ExtAuthIRuleName(virtualServer.GetUUID(), policy.GetName())
		a.app.Objects[ruleName] = &IRule{Class: classIRule, IRule: extAuthIRule(extAuthParams{
			ServicePath:     r.objectPath(a, serviceName),
			Path:            policy.GetPath(),
			TimeoutMs:       max(int(policy.GetTimeout().AsDuration().Milliseconds()), 1),
			RequestHeaders:  extAuthRequestHeaders(policy),
			UpstreamHeaders: policy.GetUpstreamHeaders(),
			ClientHeaders:   r.extAuthClientHeaders(virtualServer, policy),
			FailOpen:        policy.GetFailureMode() == route.FailureMode_ALLOW,
			StatusOnError:   policy.GetStatusOnError(),
			LogName:         virtualServer.GetName() + "/" + policy.GetName(),
		})}
		paths[policy.GetName()] = r.objectPath(a, ruleName)
	}
	return paths
}

func extAuthRequestHeaders(policy *route.ExternalAuth) []string {
	headers := []string{"authorization"}
	for _, name := range policy.GetRequestHeaders() {
		isDriverHeader := slices.Contains(extAuthDriverHeaders, name)
		if !isDriverHeader && !slices.Contains(headers, name) {
			headers = append(headers, name)
		}
	}
	return headers
}

// extAuthClientHeaders leaves out the headers HTTP::respond writes itself, and the names it would read as one of its
// options instead of a header, the header being then lost or the answer changed.
func (r *renderer) extAuthClientHeaders(virtualServer *vs.VirtualServer, policy *route.ExternalAuth) []string {
	var headers []string
	for _, name := range append(slices.Clone(policy.GetClientHeaders()), "location", "www-authenticate") {
		isAnswerHeader := slices.Contains(extAuthAnswerHeaders, name)
		isRespondOption := slices.Contains(httpRespondOptions, name) || strings.HasPrefix(name, "-")
		if isRespondOption {
			r.res.warn(virtualServer, fmt.Sprintf("external auth %s: client header %s is read as an option of HTTP::respond, it is never passed to the client",
				policy.GetName(), name))
			continue
		}
		if !isAnswerHeader && !slices.Contains(headers, name) {
			headers = append(headers, name)
		}
	}
	return headers
}
