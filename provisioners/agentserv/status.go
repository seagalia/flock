package agentserv

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *Server) ListStatus(ctx context.Context, req *agentstatus.ListStatusRequest) (*agentstatus.ListStatusResponse, error) {
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

func (s *Server) GetStatus(ctx context.Context, req *agentstatus.GetStatusRequest) (*agentstatus.StatusResponse, error) {
	prevMsg, err := stores.FindByTypeAndUuidByStore(ctx, s.prevStore, req.GetTypeUrl(), req.GetIdentifier())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	return s.statusOf(req.GetTypeUrl(), prevMsg)
}

// A resource with no state was provisioned.
func (s *Server) statusOf(typeUrl string, prevMsg proto.Message) (*agentstatus.StatusResponse, error) {
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

func (s *Server) ListResourceTypes(context.Context, *emptypb.Empty) (*agentstatus.ListResourceTypesResponse, error) {
	return &agentstatus.ListResourceTypesResponse{
		ResourceTypes: []string{helpers.TypeUrl[*cert.Certificate]()},
	}, nil
}

// GetAgentStatus reports the provisioner itself and one component per enabled provisioner kind, acme being the only
// one. A certificate in error leaves the component working, it only warns.
func (s *Server) GetAgentStatus(context.Context, *agentstatus.GetAgentStatusRequest) (*agent.AgentStatus, error) {
	var components []*agent.State
	if s.isAcmeEnabled {
		components = append(components, s.acmeState())
	}
	return &agent.AgentStatus{State: &agent.AgentStatus_SidecarAgentState{SidecarAgentState: &agent.SidecarAgentState{
		AgentState:      &agent.State{Name: s.name, Status: agent.Status_OK},
		ComponentStates: components,
	}}}, nil
}

func (s *Server) acmeState() *agent.State {
	var failures []string
	s.stateStore.Range(helpers.TypeUrl[*cert.Certificate](), func(message proto.Message, state agentstatus.StatusResponse_State, err error) {
		if state != agentstatus.StatusResponse_ERRORED {
			return
		}
		failures = append(failures, fmt.Sprintf("certificate %s: %v", helpers.GetIdentifier(message), err))
	})
	state := &agent.State{Name: "acme", Status: agent.Status_OK}
	if len(failures) == 0 {
		return state
	}
	slices.Sort(failures)
	state.Status = agent.Status_WARNING
	state.Details = strings.Join(failures, "; ")
	return state
}
