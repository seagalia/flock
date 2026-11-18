package agentserv

import (
	"context"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/samber/lo"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *AgentServer) ListStatus(ctx context.Context, req *agentstatus.ListStatusRequest) (*agentstatus.ListStatusResponse, error) {
	if req.GetTypeUrl() == helpers.TypeUrl[*backend.BackendEndpointStats]() {
		return s.listStatusEndpoints()
	}

	statuses := make([]*agentstatus.StatusResponse, 0)
	prevMsgs, err := stores.FindAllByTypeForStore(ctx, s.prevStore, req.GetTypeUrl())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	for _, prevMes := range prevMsgs {
		lastMess, state, errMess := s.stateStore.Get(req.GetTypeUrl(), helpers.GetIdentifier(prevMes))
		errMessString := ""
		// we force state to OK if it is unknown because we know that the message exists.
		if state == agentstatus.StatusResponse_UNKNOWN {
			state = agentstatus.StatusResponse_OK
		}
		if errMess != nil {
			errMessString = errMess.Error()
		}

		var erroredRes *anypb.Any
		if lastMess != nil {
			erroredRes, err = anypb.New(s.secretLinks.Unresolved(lastMess))
			if err != nil {
				return nil, connect.Errorf(connect.CodeInternal, "failed to marshal errored message: %v", err)
			}
		}

		currentRes, err := anypb.New(prevMes)
		if err != nil {
			return nil, connect.Errorf(connect.CodeInternal, "failed to marshal current message: %v", err)
		}

		statuses = append(statuses, &agentstatus.StatusResponse{
			PreviousResource: currentRes,
			ErroredResource:  erroredRes,
			State:            state,
			Error:            errMessString,
			Notes:            s.stateStore.Notes(req.GetTypeUrl(), helpers.GetIdentifier(prevMes)),
		})
	}
	return &agentstatus.ListStatusResponse{
		Statuses: statuses,
	}, nil
}

func (s *AgentServer) listStatusEndpoints() (*agentstatus.ListStatusResponse, error) {
	statuses := make([]*agentstatus.StatusResponse, 0)

	backendsEdpStats := s.edpStats.EndpointStatsByBackends()
	for _, backendStats := range backendsEdpStats {
		st, err := s.getStatusEndpoints(backendStats.GetBackendUuid())
		if err != nil {
			return nil, err
		}
		statuses = append(statuses, st)
	}
	return &agentstatus.ListStatusResponse{
		Statuses: statuses,
	}, nil
}

func (s *AgentServer) getStatusEndpoints(backendUuid string) (*agentstatus.StatusResponse, error) {
	stat := s.edpStats.EndpointStatsForBackend(backendUuid)
	if stat == nil {
		return nil, connect.Errorf(connect.CodeNotFound, "no status found for backend %s", backendUuid)
	}
	backendAny, errElem, err := s.statusEndpointToStatus(stat)
	if err != nil {
		return nil, err
	}
	state := agentstatus.StatusResponse_OK
	if errElem != "" {
		state = agentstatus.StatusResponse_ERRORED
	}
	return &agentstatus.StatusResponse{
		PreviousResource: backendAny,
		ErroredResource:  backendAny,
		State:            state,
		Error:            errElem,
	}, nil
}

func (s *AgentServer) statusEndpointToStatus(backendEdpStat *backend.BackendEndpointStats) (*anypb.Any, string, error) {
	anyMess, err := anypb.New(backendEdpStat)
	if err != nil {
		return nil, "", connect.Errorf(connect.CodeInternal, "failed to marshal message: %v", err)
	}
	inErrors := 0
	for _, edpStats := range backendEdpStat.EndpointStats {
		if !edpStats.Healthy {
			inErrors++
		}
	}
	errStatus := ""
	if inErrors > 0 {
		errStatus = fmt.Sprintf("there is %d endpoints in error", inErrors)
	}
	return anyMess, errStatus, nil
}

func (s *AgentServer) GetStatus(ctx context.Context, req *agentstatus.GetStatusRequest) (*agentstatus.StatusResponse, error) {
	if req.GetTypeUrl() == helpers.TypeUrl[*backend.BackendEndpointStats]() {
		return s.getStatusEndpoints(req.GetIdentifier())
	}

	erroredMess, state, errMess := s.stateStore.Get(req.GetTypeUrl(), req.GetIdentifier())
	prevMes, err := stores.FindByTypeAndUuidByStore(ctx, s.prevStore, req.GetTypeUrl(), req.GetIdentifier())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}

	var erroredRes *anypb.Any
	if erroredMess != nil {
		erroredRes, err = anypb.New(s.secretLinks.Unresolved(erroredMess))
		if err != nil {
			return nil, connect.Errorf(connect.CodeInternal, "failed to marshal errored message: %v", err)
		}
	}

	currentRes, err := anypb.New(prevMes)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to marshal current message: %v", err)
	}

	errMessString := ""
	// we force state to OK if it is unknown because we know that the message exists.
	if state == agentstatus.StatusResponse_UNKNOWN {
		state = agentstatus.StatusResponse_OK
	}
	if errMess != nil {
		errMessString = errMess.Error()
	}

	return &agentstatus.StatusResponse{
		PreviousResource: currentRes,
		ErroredResource:  erroredRes,
		State:            state,
		Error:            errMessString,
		Notes:            s.stateStore.Notes(req.GetTypeUrl(), req.GetIdentifier()),
	}, nil
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

func (s *AgentServer) listAnnouncingVips(ctx context.Context) ([]*vip.VIP, error) {
	allVips, err := s.vips.List(ctx, nil)
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	if s.vipPlacer == nil {
		return s.vips.List(ctx, nil)
	}
	vipPlacedOn := s.vipPlacer.ListPlaceOn()
	vipsPlaced := s.vipPlacer.AnnouncedVips()
	vipsAnnonced := make([]*vip.VIP, 0)

	for _, v := range allVips {
		ann, err := v.GetStarlingAnnounce()
		if err != nil {
			s.entry.Error().Msgf("failed to get agent announce: %s", err.Error())
			continue
		}
		if !lo.ContainsBy[starling_announce.StarlingAnnounce_TypeAnnounce](vipPlacedOn, func(item starling_announce.StarlingAnnounce_TypeAnnounce) bool {
			return ann.GetTypeAnnounce() == item
		}) {
			vipsAnnonced = append(vipsAnnonced, v)
			continue
		}
		if lo.ContainsBy[*vip.VIP](vipsPlaced, func(item *vip.VIP) bool {
			return item.GetUUID() == v.GetUUID()
		}) {
			vipsAnnonced = append(vipsAnnonced, v)
		}
	}
	return vipsAnnonced, nil
}

func (s *AgentServer) GetAgentStatus(ctx context.Context, req *agentstatus.GetAgentStatusRequest) (*agent.AgentStatus, error) {
	var announcingVips []*vip.VIP
	if req.GetShowAnnouncingVips() {
		var err error
		announcingVips, err = s.listAnnouncingVips(ctx)
		if err != nil {
			return nil, err
		}
		for i, announcingVip := range announcingVips {
			announcingVips[i] = s.secretLinks.Unresolved(announcingVip).(*vip.VIP)
		}
	}

	return &agent.AgentStatus{
		State: &agent.AgentStatus_SidecarAgentState{
			SidecarAgentState: &agent.SidecarAgentState{
				AgentState: &agent.State{
					Name:    s.name,
					Status:  agent.Status_OK,
					Details: "",
				},
				ComponentStates: []*agent.State{
					{
						Name:   "ipvs",
						Status: agent.Status_OK,
					},
					componentState("envoy", s.isEnvoyAlive),
					componentState("waf", s.isWafAlive),
				},
				AnnouncingVips: announcingVips,
			},
		},
	}, nil
}

func componentState(name string, isAlive func() error) *agent.State {
	err := isAlive()
	if err != nil {
		return &agent.State{Name: name, Status: agent.Status_ERRORED, Details: err.Error()}
	}
	return &agent.State{Name: name, Status: agent.Status_OK}
}
