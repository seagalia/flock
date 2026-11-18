package models

import (
	"fmt"
	"slices"
	"strings"

	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"google.golang.org/protobuf/proto"
)

type Auth struct {
	// io.seagalia.flock.auth.policy, name of the external auth policy checking the route, declared by the
	// io.seagalia.flock.auth.provide.name of another container, http and http2 only
	Policy string `mapstructure:"policy"`
	// io.seagalia.flock.auth.provide.*
	Provide *AuthProvide `mapstructure:"provide"`
}

// AuthProvide makes the container an auth service: the provisioner copies its policy into every virtual server holding
// a route that names it.
type AuthProvide struct {
	// io.seagalia.flock.auth.provide.name, name of the policy, unique among the containers
	Name string `mapstructure:"name"`
	// io.seagalia.flock.auth.provide.path, exact path of the check request, / when unset
	Path string `mapstructure:"path"`
	// io.seagalia.flock.auth.provide.request_headers, comma-separated headers of the client request sent to the check
	RequestHeaders Headers `mapstructure:"request_headers"`
	// io.seagalia.flock.auth.provide.upstream_headers, comma-separated headers of an allow answer set on the request
	// to the backend
	UpstreamHeaders Headers `mapstructure:"upstream_headers"`
	// io.seagalia.flock.auth.provide.client_headers, comma-separated headers of a deny answer sent to the client
	ClientHeaders Headers `mapstructure:"client_headers"`
	// io.seagalia.flock.auth.provide.timeout, e.g. 2s, 1s when unset
	Timeout *Duration `mapstructure:"timeout"`
	// io.seagalia.flock.auth.provide.failure_mode, deny|allow, deny when unset
	FailureMode *FailureMode `mapstructure:"failure_mode"`
	// io.seagalia.flock.auth.provide.status_on_error, status sent when the check can't answer, 503 when unset
	StatusOnError uint32 `mapstructure:"status_on_error"`
}

// RemoveExternalAuths answers a copy of stored without the policies checked by backendUuid. The caller removes the
// routes naming them too, the controller refuses a route naming a policy the virtual server doesn't define.
func RemoveExternalAuths(stored *vs.VirtualServer, backendUuid string) *vs.VirtualServer {
	cleaned := proto.CloneOf(stored)
	httpConfig := cleaned.GetHttpRoutesConfig()
	if httpConfig == nil {
		return cleaned
	}
	httpConfig.ExternalAuths = slices.DeleteFunc(httpConfig.ExternalAuths, func(policy *route.ExternalAuth) bool {
		return policy.GetBackendUuid() == backendUuid
	})
	return cleaned
}

type Headers []string

func (h *Headers) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}
	for header := range strings.SplitSeq(str, ",") {
		header = strings.TrimSpace(header)
		if header == "" {
			continue
		}
		*h = append(*h, header)
	}
	return nil
}

type FailureMode route.FailureMode

func (f *FailureMode) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}
	mode, ok := route.FailureMode_value[strings.ToUpper(str)]
	if !ok {
		return fmt.Errorf("invalid failure mode: %s", str)
	}
	*f = FailureMode(mode)
	return nil
}
