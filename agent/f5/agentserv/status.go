package agentserv

import (
	"context"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"

	"connectrpc.com/connect/v2"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func (s *AgentServer) ListStatus(ctx context.Context, req *agentstatus.ListStatusRequest) (*agentstatus.ListStatusResponse, error) {
	if req.GetTypeUrl() == helpers.TypeUrl[*backend.BackendEndpointStats]() {
		return s.listEndpointStats(ctx)
	}
	prevMsgs, err := stores.FindAllByTypeForStore(ctx, s.prevStore, req.GetTypeUrl())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	statuses := make([]*agentstatus.StatusResponse, 0, len(prevMsgs))
	for _, prevMsg := range prevMsgs {
		status, err := s.statusOf(req.GetTypeUrl(), prevMsg)
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return &agentstatus.ListStatusResponse{Statuses: statuses}, nil
}

func (s *AgentServer) GetStatus(ctx context.Context, req *agentstatus.GetStatusRequest) (*agentstatus.StatusResponse, error) {
	if req.GetTypeUrl() == helpers.TypeUrl[*backend.BackendEndpointStats]() {
		return s.endpointStatus(ctx, req.GetIdentifier())
	}
	prevMsg, err := stores.FindByTypeAndUuidByStore(ctx, s.prevStore, req.GetTypeUrl(), req.GetIdentifier())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	return s.statusOf(req.GetTypeUrl(), prevMsg)
}

// A resource with no state was applied.
func (s *AgentServer) statusOf(typeUrl string, prevMsg proto.Message) (*agentstatus.StatusResponse, error) {
	uuid := helpers.GetIdentifier(prevMsg)
	erroredMsg, state, stateErr := s.stateStore.Get(typeUrl, uuid)
	if state == agentstatus.StatusResponse_UNKNOWN {
		state = agentstatus.StatusResponse_OK
	}
	status := &agentstatus.StatusResponse{State: state, Notes: s.stateStore.Notes(typeUrl, uuid)}
	if stateErr != nil {
		status.Error = stateErr.Error()
	}
	var err error
	status.PreviousResource, err = anypb.New(prevMsg)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to marshal current message: %v", err)
	}
	if erroredMsg != nil {
		status.ErroredResource, err = anypb.New(s.secretLinks.Unresolved(erroredMsg))
		if err != nil {
			return nil, connect.Errorf(connect.CodeInternal, "failed to marshal errored message: %v", err)
		}
	}
	return status, nil
}

func (s *AgentServer) listEndpointStats(ctx context.Context) (*agentstatus.ListStatusResponse, error) {
	backends, err := s.backends.List(ctx, nil)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	statuses := make([]*agentstatus.StatusResponse, 0)
	for _, b := range backends {
		if len(s.rec.PoolsOfBackend(b.GetUUID())) == 0 {
			continue
		}
		status, err := s.endpointStatus(ctx, b.GetUUID())
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, status)
	}
	return &agentstatus.ListStatusResponse{Statuses: statuses}, nil
}

// endpointStatus reads the member states of every pool built from a backend, one per VIP using it. An endpoint is
// healthy when it is up in all of them.
func (s *AgentServer) endpointStatus(ctx context.Context, backendUUID string) (*agentstatus.StatusResponse, error) {
	pools := s.rec.PoolsOfBackend(backendUUID)
	if len(pools) == 0 {
		return nil, connect.Errorf(connect.CodeNotFound, "backend %s has no pool on the BIG-IP", backendUUID)
	}
	healthy := map[string]bool{}
	lastErr := map[string]string{}
	for _, pool := range pools {
		states, err := s.dev.PoolMemberStates(ctx, pool.Tenant, pool.App, pool.Pool)
		if err != nil {
			return nil, connect.Errorf(connect.CodeUnavailable, "reading the members of pool /%s/%s/%s: %v", pool.Tenant, pool.App, pool.Pool, err)
		}
		for member, up := range states {
			wasHealthy, seen := healthy[member]
			healthy[member] = up && (!seen || wasHealthy)
			if !up {
				lastErr[member] = fmt.Sprintf("down in pool /%s/%s/%s", pool.Tenant, pool.App, pool.Pool)
			}
		}
	}

	stats := &backend.BackendEndpointStats{BackendUuid: backendUUID, Healthy: true}
	unhealthy := 0
	for _, member := range slices.Sorted(maps.Keys(healthy)) {
		stat := &backend.EndpointStat{IpAddr: ipAddrOf(member), Healthy: healthy[member]}
		if !healthy[member] {
			stat.LastError = wrapperspb.String(lastErr[member])
			stats.Healthy = false
			unhealthy++
		}
		stats.EndpointStats = append(stats.EndpointStats, stat)
	}
	statsAny, err := anypb.New(stats)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to marshal message: %v", err)
	}
	status := &agentstatus.StatusResponse{PreviousResource: statsAny, ErroredResource: statsAny, State: agentstatus.StatusResponse_OK}
	if unhealthy > 0 {
		status.State = agentstatus.StatusResponse_ERRORED
		status.Error = fmt.Sprintf("there is %d endpoints in error", unhealthy)
	}
	return status, nil
}

func ipAddrOf(member string) *core.IPAddr {
	host, portText, err := net.SplitHostPort(member)
	if err != nil {
		return &core.IPAddr{Ip: member}
	}
	port, _ := strconv.ParseUint(portText, 10, 32)
	return &core.IPAddr{Ip: host, Port: uint32(port)}
}

func (s *AgentServer) ListResourceTypes(context.Context, *emptypb.Empty) (*agentstatus.ListResourceTypesResponse, error) {
	return &agentstatus.ListResourceTypesResponse{
		ResourceTypes: []string{
			helpers.TypeUrl[*backend.Backend](),
			helpers.TypeUrl[*vip.VIP](),
			helpers.TypeUrl[*vs.VirtualServer](),
			helpers.TypeUrl[*cert.Certificate](),
			helpers.TypeUrl[*backend.BackendEndpointStats](),
		},
	}, nil
}
