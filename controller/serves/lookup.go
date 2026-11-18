package serves

import (
	"cmp"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/hashicorp/go-multierror"
	"github.com/samber/lo"
	"github.com/seagalia/flock/sdk/contexes"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1/agentstatusconnect"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	CtxInstanceUuidKey = "ctx_instance_uuid"

	// defaultAgentCallTimeout bounds a unary call to an agent, one that accepts the connection and never answers would
	// otherwise hold the request of the user until the user gives up
	defaultAgentCallTimeout = 10 * time.Second
)

// values of provisionerPolicy, in a block of their own so iota starts at zero
const (
	withProvisioners provisionerPolicy = iota
	withoutProvisionersPolicy
)

// agentCallTimeout is a variable so the specs can shorten it
var agentCallTimeout = defaultAgentCallTimeout

// agentCallContext gives the context of a call to an agent for instanceUuid. Each call needs its own: the request
// headers live in a CallInfo connect attaches to the context, which concurrent calls can't share.
func agentCallContext(ctx context.Context, instanceUuid string) context.Context {
	ctx, callInfo := connect.NewClientContext(ctx)
	callInfo.RequestHeader().Set(CtxInstanceUuidKey, instanceUuid)
	return ctx
}

// agentUnaryCallContext is agentCallContext bounded by agentCallTimeout, for the calls that are not streams
func agentUnaryCallContext(ctx context.Context, instanceUuid string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, agentCallTimeout)
	return agentCallContext(ctx, instanceUuid), cancel
}

// isRPCError tells if err already is an error for the client of the call, with its code
func isRPCError(err error) bool {
	var connectErr *connect.Error
	return stderrors.As(err, &connectErr)
}

func (s *FlockServer) GetLogsStream(ctx context.Context, req *svclookup.GetLogsRequest, fs svclookupconnect.LookupGetLogsStreamServerStream) error {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetInstanceUuid(), permission.Role_READER)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup

	agentList, err := s.findAgentsLoggable(ctx, req.GetInstanceUuid(), req.GetAgentUuid())
	if err != nil {
		if isRPCError(err) {
			return err
		}
		return connect.Errorf(connect.CodeInternal, "failed to get agentListener list from instance %s, error: %v", req.GetInstanceUuid(), err).WithCause(err)
	}

	if len(agentList) == 0 {
		return connect.Errorf(connect.CodeUnimplemented, "no agentListener found with log feature")
	}
	agentList, err = s.agentsToAsk(ctx, agentList)
	if err != nil {
		return err
	}

	started := &svclookup.LogsStreamResponse{Event: &svclookup.LogsStreamResponse_Started{Started: &svclookup.LogsStreamResponse_StreamStarted{}}}
	if err := fs.Send(started); err != nil {
		return nil // the client went away before the first message
	}

	var errChan = make(chan error, len(agentList))
	sendMutex := &sync.Mutex{}
	for _, agentListener := range agentList {
		client := agentlogsconnect.NewLogsClient(s.agentClients.Client(agentListener.GetHost()))
		logRequest, err := client.Follow(agentCallContext(ctx, req.GetInstanceUuid()), &emptypb.Empty{})
		if err != nil {
			s.sendErrToLogStream(
				sendMutex,
				fs,
				agentListener.GetUUID(),
				fmt.Errorf("failed to get logs from agentListener, error: %w", err),
			)
			continue
		}
		wg.Add(1)
		go func(agentListener *agent.Agent) {
			defer wg.Done()
			defer logRequest.Close() // nolint:errcheck
			for {
				log, err := logRequest.Receive()
				if stderrors.Is(err, io.EOF) {
					return
				}
				if err != nil {
					if connect.CodeOf(err) != connect.CodeCanceled {
						s.sendErrToLogStream(sendMutex, fs, agentListener.GetUUID(), err)
					}
					return
				}
				// the client got its own started message, the ones of the agents carry no line
				if log.GetStarted() != nil {
					continue
				}

				sendMutex.Lock()
				err = fs.Send(agentLogLine(agentListener.GetUUID(), log.GetLogLine()))
				sendMutex.Unlock()
				if err != nil {
					isClientGone := connect.CodeOf(err) == connect.CodeCanceled || ctx.Err() != nil
					if !isClientGone {
						errChan <- connect.Errorf(connect.CodeInternal, "failed to send logs to client, error: %v", err).WithCause(err)
					}
					return
				}
			}
		}(agentListener)
	}
	go func() {
		wg.Wait()
		close(errChan)
	}()

	var multiErr error
	for err = range errChan {
		multiErr = multierror.Append(multiErr, err)
	}

	if multiErr != nil {
		return multiErr
	}
	return nil
}

// provisionerPolicy tells if a lookup of every agent of an instance includes its provisioners, the status calls of a
// load balancer must not reach them
type provisionerPolicy int

// findAgents gives the agent of agentUuid, or every agent of the instance when it is empty
func (s *FlockServer) findAgents(ctx context.Context, instanceUuid, agentUuid string, policy provisionerPolicy) ([]*agent.Agent, error) {
	if agentUuid == "" {
		mapAgent := make(map[string]*agent.Agent)
		agents, err := s.instantiater.ListAgents(ctx, instanceUuid)
		if err != nil {
			return nil, err
		}
		for _, a := range agents {
			if isProvisioner(a) && policy == withoutProvisionersPolicy {
				continue
			}
			mapAgent[a.GetUUID()] = a
		}
		if len(mapAgent) == 0 {
			return nil, fmt.Errorf("no agentListener found in instance %s", instanceUuid)
		}
		agentList := make([]*agent.Agent, 0)
		for _, a := range mapAgent {
			agentList = append(agentList, a)
		}
		sortAgents(agentList)
		return agentList, nil
	}
	agents, err := s.instantiater.ListAgents(ctx, instanceUuid)
	if err != nil {
		return nil, err
	}
	for _, a := range agents {
		if a.GetUUID() == agentUuid {
			return []*agent.Agent{a}, nil
		}
	}
	return nil, fmt.Errorf("agent %s not found", agentUuid)
}

func (s *FlockServer) sendErrToLogStream(mutex *sync.Mutex, fs svclookupconnect.LookupGetLogsStreamServerStream, agentUuid string, err error) {
	mutex.Lock()
	defer mutex.Unlock()
	// nolint:errcheck
	fs.Send(agentLogLine(agentUuid, s.errToLogErr(err)))
}

func agentLogLine(agentUuid, line string) *svclookup.LogsStreamResponse {
	return &svclookup.LogsStreamResponse{Event: &svclookup.LogsStreamResponse_LogStream{
		LogStream: &svclookup.LogsStreamResponse_AgentLogLine{AgentUuid: agentUuid, LogLine: line},
	}}
}

func (s *FlockServer) errToLogErr(err error) string {
	now := time.Now().Format("2006-01-02 15:04:05")
	return fmt.Sprintf("\033[1;31mErr[%s]\033[0m %s\n", now, err.Error())
}

func (s *FlockServer) findAgentsLoggable(ctx context.Context, instanceUuid, agentUuid string) ([]*agent.Agent, error) {
	agentList, err := s.findAgents(ctx, instanceUuid, agentUuid, withProvisioners)
	if err != nil {
		return nil, err
	}
	finalAgentList := make([]*agent.Agent, 0)
	for _, agentListener := range agentList {
		if agentListener.GetFeatures().GetShowLogs() {
			finalAgentList = append(finalAgentList, agentListener)
		}
	}
	if len(finalAgentList) == 0 {
		return nil, connect.Errorf(connect.CodeUnimplemented, "no agentListener found with log feature")
	}
	return finalAgentList, nil
}

func (s *FlockServer) findAgentsStatusable(ctx context.Context, instanceUuid, agentUuid string, policy provisionerPolicy) ([]*agent.Agent, error) {
	agentList, err := s.findAgents(ctx, instanceUuid, agentUuid, policy)
	if err != nil {
		return nil, err
	}
	finalAgentList := make([]*agent.Agent, 0)
	for _, agentListener := range agentList {
		if agentListener.GetFeatures().GetShowStatus() {
			finalAgentList = append(finalAgentList, agentListener)
		}
	}
	if len(finalAgentList) == 0 {
		return nil, connect.Errorf(connect.CodeUnimplemented, "no agentListener found with status feature")
	}
	return finalAgentList, nil
}

func (s *FlockServer) GetLogsLatest(ctx context.Context, req *svclookup.GetLogsRequest) (*svclookup.LogsResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetInstanceUuid(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	agentList, err := s.findAgentsLoggable(ctx, req.GetInstanceUuid(), req.GetAgentUuid())
	if err != nil {
		if isRPCError(err) {
			return nil, err
		}
		return nil, connect.Errorf(connect.CodeInternal, "failed to get agentListener list from instance %s, error code: %v", req.GetInstanceUuid(), err).WithCause(err)
	}
	if len(agentList) == 0 {
		return nil, connect.Errorf(connect.CodeUnimplemented, "no agentListener found with log feature")
	}
	agentList, err = s.agentsToAsk(ctx, agentList)
	if err != nil {
		return nil, err
	}
	logsResponse := &svclookup.LogsResponse{
		Logs: make([]*svclookup.LogsResponse_LogResponse, 0),
	}
	for _, agentListener := range agentList {
		client := agentlogsconnect.NewLogsClient(s.agentClients.Client(agentListener.GetHost()))
		callCtx, cancel := agentUnaryCallContext(ctx, req.GetInstanceUuid())
		logRequest, err := client.Latest(callCtx, &emptypb.Empty{})
		cancel()
		if err != nil {
			err = fmt.Errorf("failed to connect to agentListener, error: %w", err)
			logsResponse.Logs = append(logsResponse.Logs, &svclookup.LogsResponse_LogResponse{
				AgentUuid: agentListener.GetUUID(),
				LogLines:  []string{s.errToLogErr(err)},
			})
			continue
		}
		logsResponse.Logs = append(logsResponse.Logs, &svclookup.LogsResponse_LogResponse{
			AgentUuid: agentListener.GetUUID(),
			LogLines:  logRequest.GetLogLines(),
		})
	}

	return logsResponse, nil
}

func (s *FlockServer) GetStatus(ctx context.Context, req *svclookup.GetStatusRequest) (*svclookup.StatusResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetInstanceUuid(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	allAgents, err := s.findAgentsStatusable(ctx, req.GetInstanceUuid(), req.GetAgentUuid(), withoutProvisionersPolicy)
	if err != nil {
		if isRPCError(err) {
			return nil, err
		}
		return nil, connect.Errorf(connect.CodeInternal, "failed to get agentListener list from instance %s, error code: %v", req.GetInstanceUuid(), err).WithCause(err)
	}
	agentListener, err := s.firstReachableAgent(ctx, allAgents)
	if err != nil {
		return nil, err
	}

	client := agentstatusconnect.NewResourceStatusClient(s.agentClients.Client(agentListener.GetHost()))
	callCtx, cancel := agentUnaryCallContext(ctx, req.GetInstanceUuid())
	defer cancel()
	statusResponse, err := client.GetStatus(
		callCtx,
		&agentstatus.GetStatusRequest{
			TypeUrl:    req.GetTypeUrl(),
			Identifier: req.GetIdentifier(),
		})
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to get status from agentListener %s, error: %v", agentListener, err).WithCause(err)
	}

	return &svclookup.StatusResponse{
		InstanceUuid:     req.GetInstanceUuid(),
		AgentUuid:        req.GetAgentUuid(),
		PreviousResource: statusResponse.GetPreviousResource(),
		ErroredResource:  statusResponse.GetErroredResource(),
		State:            svclookup.StatusResponse_State(statusResponse.GetState()),
		Error:            statusResponse.GetError(),
		Notes:            statusResponse.GetNotes(),
	}, nil
}

func (s *FlockServer) ListStatus(ctx context.Context, req *svclookup.ListStatusRequest) (*svclookup.ListStatusResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetInstanceUuid(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	allAgents, err := s.findAgentsStatusable(ctx, req.GetInstanceUuid(), req.GetAgentUuid(), withoutProvisionersPolicy)
	if err != nil {
		if isRPCError(err) {
			return nil, err
		}
		return nil, connect.Errorf(connect.CodeInternal, "failed to get agentListener list from instance %s, error code: %v", req.GetInstanceUuid(), err).WithCause(err)
	}
	agentListener, err := s.firstReachableAgent(ctx, allAgents)
	if err != nil {
		return nil, err
	}

	client := agentstatusconnect.NewResourceStatusClient(s.agentClients.Client(agentListener.GetHost()))
	callCtx, cancel := agentUnaryCallContext(ctx, req.GetInstanceUuid())
	defer cancel()
	statusResponse, err := client.ListStatus(
		callCtx,
		&agentstatus.ListStatusRequest{
			TypeUrl: req.GetTypeUrl(),
		})
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal,
			"failed to get status from agentListener %s, error: %s", agentListener, err.Error())
	}

	statuses := make([]*svclookup.StatusResponse, 0)
	for _, st := range statusResponse.GetStatuses() {
		statuses = append(statuses, &svclookup.StatusResponse{
			InstanceUuid:     req.GetInstanceUuid(),
			AgentUuid:        req.GetAgentUuid(),
			PreviousResource: st.GetPreviousResource(),
			ErroredResource:  st.GetErroredResource(),
			State:            svclookup.StatusResponse_State(st.GetState()),
			Error:            st.GetError(),
			Notes:            st.GetNotes(),
		})
	}
	return &svclookup.ListStatusResponse{
		Status: statuses,
	}, nil
}

func (s *FlockServer) ListAgent(ctx context.Context, req *svclookup.ListAgentRequest) (*svclookup.ListAgentResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetInstanceUuid(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	// an instance with no agent yet answers an empty list, the callers show it as such
	agents, err := s.instantiater.ListAgents(ctx, req.GetInstanceUuid())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to list the agents of instance %s: %v", req.GetInstanceUuid(), err).WithCause(err)
	}
	agentList := slices.Clone(agents)
	sortAgents(agentList)
	return &svclookup.ListAgentResponse{
		Agents: agentList,
	}, nil
}

func provisionerRank(ag *agent.Agent) int {
	if isProvisioner(ag) {
		return 1
	}
	return 0
}

// sortAgents orders the agents then the provisioners, each by name then uuid, so listings keep the same order from one
// call to the next
func sortAgents(agents []*agent.Agent) {
	slices.SortFunc(agents, func(a, b *agent.Agent) int {
		return cmp.Or(
			cmp.Compare(provisionerRank(a), provisionerRank(b)),
			cmp.Compare(a.GetName(), b.GetName()),
			cmp.Compare(a.GetUUID(), b.GetUUID()),
		)
	})
}

func (s *FlockServer) ListInstanceNodes(ctx context.Context, req *svclookup.ListInstanceNodesRequest) (*svclookup.ListInstanceNodesResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetInstanceUuid(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	nodes, err := s.storeMan.Nodes().ListNodes(ctx, req.GetInstanceUuid())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to list the nodes of instance %s: %v", req.GetInstanceUuid(), err).WithCause(err)
	}
	return &svclookup.ListInstanceNodesResponse{Nodes: nodes}, nil
}

func (s *FlockServer) ListResourceTypesStatus(ctx context.Context, req *svclookup.ListResourceTypesRequest) (*svclookup.ListResourceTypesStatusResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetInstanceUuid(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	agentList, err := s.findAgents(ctx, req.GetInstanceUuid(), req.GetAgentUuid(), withoutProvisionersPolicy)
	if err != nil {
		if isRPCError(err) {
			return nil, err
		}
		return nil, connect.Errorf(connect.CodeInternal, "failed to get agentListener list from instance %s, error code: %v", req.GetInstanceUuid(), err).WithCause(err)
	}
	ag, err := s.firstReachableAgent(ctx, agentList)
	if err != nil {
		return nil, err
	}
	client := agentstatusconnect.NewResourceStatusClient(s.agentClients.Client(ag.GetHost()))
	callCtx, cancel := agentUnaryCallContext(ctx, req.GetInstanceUuid())
	defer cancel()
	resp, err := client.ListResourceTypes(callCtx, &emptypb.Empty{})
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal,
			"failed to get status from agent %s, error code: %s", ag, err.Error())
	}

	return &svclookup.ListResourceTypesStatusResponse{
		ResourceTypes: resp.GetResourceTypes(),
	}, nil
}

func (s *FlockServer) ListAgentStatus(ctx context.Context, req *svclookup.ListAgentStatusRequest) (*svclookup.ListAgentStatusResponse, error) {
	err := s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), req.GetInstanceUuid(), permission.Role_READER)
	if err != nil {
		return nil, err
	}

	allAgents, err := s.findAgentsStatusable(ctx, req.GetInstanceUuid(), req.GetAgentUuid(), withProvisioners)
	if err != nil {
		if isRPCError(err) {
			return nil, err
		}
		return nil, connect.Errorf(connect.CodeInternal, "failed to get agentListener list from instance %s, error code: %v", req.GetInstanceUuid(), err).WithCause(err)
	}
	if len(allAgents) == 0 {
		return &svclookup.ListAgentStatusResponse{}, nil
	}

	if isBridgeAgents(allAgents) {
		// an unreachable bridge keeps its error entry, the dashboard shows the status of each agent
		bridge, err := s.firstReachableAgent(ctx, withoutProvisioners(allAgents))
		if err != nil {
			bridge = withoutProvisioners(allAgents)[0]
		}
		allAgents = append([]*agent.Agent{bridge}, onlyProvisioners(allAgents)...)
	}
	// each goroutine fills its own slot so the answer keeps the order of the agents
	agentStatuses := make([]*svclookup.GetAgentStatusResponse, len(allAgents))
	wg := &sync.WaitGroup{}
	for i, ag := range allAgents {
		wg.Go(func() {
			client := agentstatusconnect.NewAgentStatusClient(s.agentClients.Client(ag.GetHost()))
			callCtx, cancel := agentUnaryCallContext(ctx, req.GetInstanceUuid())
			defer cancel()
			statusResponse, err := client.GetAgentStatus(
				callCtx,
				&agentstatus.GetAgentStatusRequest{ShowAnnouncingVips: req.GetShowAnnouncingVips()},
			)
			if err != nil {
				agentStatuses[i] = errToErrAgentStatus(ag, req.GetInstanceUuid(), err)
				return
			}
			agentStatuses[i] = &svclookup.GetAgentStatusResponse{
				InstanceUuid: req.GetInstanceUuid(),
				AgentUuid:    ag.UUID,
				AgentStatus:  statusResponse,
			}
		})
	}
	wg.Wait()

	for _, agentStatus := range agentStatuses {
		err := s.filterAnnouncingVips(ctx, agentStatus)
		if err != nil {
			return nil, connect.Errorf(connect.CodeInternal,
				"failed to filter announcing vips from agent %s, error code: %s",
				agentStatus.GetAgentUuid(), err.Error(),
			)
		}
	}

	return &svclookup.ListAgentStatusResponse{
		AgentStatus: agentStatuses,
	}, nil

}

func (s *FlockServer) filterAnnouncingVips(ctx context.Context, resp *svclookup.GetAgentStatusResponse) error {
	if resp.GetAgentStatus().GetSidecarAgentState() == nil && resp.GetAgentStatus().GetProxyAgentState() == nil {
		return nil
	}
	if resp.GetAgentStatus().GetSidecarAgentState() != nil &&
		len(resp.GetAgentStatus().GetSidecarAgentState().AnnouncingVips) == 0 {
		return nil
	}
	if resp.GetAgentStatus().GetProxyAgentState() != nil &&
		len(resp.GetAgentStatus().GetProxyAgentState().GetComponentAnnouncingVips()) == 0 {
		return nil
	}
	if s.checkIsAdmin(ctx) == nil {
		return nil
	}
	username, groups, _ := contexes.RetrieveUserInfo(ctx)
	vipList, err := s.storeMan.VIPs().List(ctx, &storers.ListOptions{
		UserName:   username,
		UserGroups: groups,
	})
	if err != nil {
		return errors.ToConnectError(err)
	}

	if resp.GetAgentStatus().GetSidecarAgentState() != nil {
		agentState := resp.GetAgentStatus().GetSidecarAgentState()
		agentState.AnnouncingVips = lo.Filter[*vip.VIP](agentState.AnnouncingVips, func(v *vip.VIP, index int) bool {
			return lo.ContainsBy[*vip.VIP](vipList, func(userVip *vip.VIP) bool {
				return v.GetUUID() == userVip.GetUUID()
			})
		})
		return nil
	}
	agentState := resp.GetAgentStatus().GetProxyAgentState()
	for _, compAnon := range agentState.GetComponentAnnouncingVips() {
		compAnon.AnnouncingVips = lo.Filter[*vip.VIP](compAnon.AnnouncingVips, func(v *vip.VIP, index int) bool {
			return lo.ContainsBy[*vip.VIP](vipList, func(userVip *vip.VIP) bool {
				return v.GetUUID() == userVip.GetUUID()
			})
		})
	}
	return nil
}

func errToErrAgentStatus(ag *agent.Agent, instanceUuid string, err error) *svclookup.GetAgentStatusResponse {
	agentState := &agent.AgentStatus{
		State: &agent.AgentStatus_SidecarAgentState{
			SidecarAgentState: &agent.SidecarAgentState{
				AgentState: &agent.State{
					Name:    ag.Name,
					Status:  agent.Status_ERRORED,
					Details: err.Error(),
				},
			},
		}}
	if isBridgeAgent(ag) {
		agentState = &agent.AgentStatus{
			State: &agent.AgentStatus_ProxyAgentState{
				ProxyAgentState: &agent.BridgeAgentStatus{
					ComponentStates: []*agent.State{
						{
							Name:    ag.Name,
							Status:  agent.Status_UNKNOWN,
							Details: err.Error(),
						},
					},
				},
			},
		}
	}
	return &svclookup.GetAgentStatusResponse{
		InstanceUuid: instanceUuid,
		AgentUuid:    ag.UUID,
		AgentStatus:  agentState,
	}
}

func (s *FlockServer) ReadAgent(ctx context.Context, req *svclookup.ReadAgentRequest) (*svclookup.ReadAgentResponse, error) {
	instanceUuid, err := s.storeMan.Nodes().FindNodeInstance(ctx, req.GetAgentUuid())
	if err != nil {
		return nil, errors.ToConnectError(err)
	}
	err = s.checkPermission(ctx, helpers.TypeUrl[*instance.Instance](), instanceUuid, permission.Role_READER)
	if err != nil {
		return nil, err
	}

	agents, err := s.instantiater.ListAgents(ctx, instanceUuid)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInternal, "failed to list the agents of instance %s: %v", instanceUuid, err).WithCause(err)
	}
	for _, ag := range agents {
		if ag.GetUUID() == req.GetAgentUuid() {
			return &svclookup.ReadAgentResponse{Agent: ag, InstanceUuid: instanceUuid}, nil
		}
	}
	return nil, connect.Errorf(connect.CodeNotFound, "agent %s is no longer declared by instance %s", req.GetAgentUuid(), instanceUuid)
}
