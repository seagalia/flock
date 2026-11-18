package controller

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	agentapi "github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
)

const instancesPageSize = 100

type instanceLister interface {
	ListInstance(ctx context.Context, req *svcinstance.ListInstanceRequest) (*svcinstance.ListInstanceResponse, error)
}

type statusLookup interface {
	ListAgent(ctx context.Context, req *svclookup.ListAgentRequest) (*svclookup.ListAgentResponse, error)
	ListStatus(ctx context.Context, req *svclookup.ListStatusRequest) (*svclookup.ListStatusResponse, error)
}

// AppliedState is how the agents of every instance applied one resource: the worst state among them, and the error of
// each agent that failed.
type AppliedState struct {
	State svclookup.StatusResponse_State
	// "<agent uuid>: <error>", sorted
	Errors []string
}

// AgentStatusPoller asks flock how the agents applied the resources of one type, since flock streams no status change,
// and sends the uuid of every resource whose state changed so its object is reconciled again.
type AgentStatusPoller struct {
	instances instanceLister
	lookup    statusLookup
	typeUrl   string
	interval  time.Duration
	changed   chan<- string

	mu sync.Mutex
	// by resource uuid, a resource no agent holds is absent
	states map[string]AppliedState
}

func NewAgentStatusPoller(
	instances instanceLister, lookup statusLookup, typeUrl string, interval time.Duration, changed chan<- string,
) *AgentStatusPoller {
	return &AgentStatusPoller{
		instances: instances,
		lookup:    lookup,
		typeUrl:   typeUrl,
		interval:  interval,
		changed:   changed,
		states:    map[string]AppliedState{},
	}
}

// NeedLeaderElection keeps the poller on the leader, the only replica whose reconcilers read its states.
func (p *AgentStatusPoller) NeedLeaderElection() bool {
	return true
}

// Start polls until ctx ends, as a Runnable of the manager.
func (p *AgentStatusPoller) Start(ctx context.Context) error {
	for {
		roundCtx, cancel := context.WithTimeout(ctx, p.interval)
		err := p.poll(roundCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			logf.FromContext(ctx).Error(err, "poll the agent statuses", "typeUrl", p.typeUrl)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(p.interval):
		}
	}
}

// State gives how the agents applied the resource, false when none of them holds it.
func (p *AgentStatusPoller) State(uuid string) (AppliedState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.states[uuid]
	return state, ok
}

// poll keeps the states of the last round when one call fails: an agent missing from a round would turn its errors
// into a recovery.
func (p *AgentStatusPoller) poll(ctx context.Context) error {
	states, err := p.collect(ctx)
	if err != nil {
		return err
	}

	p.mu.Lock()
	var changed []string
	for uuid, state := range states {
		previous, existed := p.states[uuid]
		if !existed || !sameState(previous, state) {
			changed = append(changed, uuid)
		}
	}
	for uuid := range p.states {
		if _, still := states[uuid]; !still {
			changed = append(changed, uuid)
		}
	}
	p.states = states
	p.mu.Unlock()

	for _, uuid := range changed {
		select {
		case p.changed <- uuid:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (p *AgentStatusPoller) collect(ctx context.Context) (map[string]AppliedState, error) {
	instanceUuids, err := p.listInstances(ctx)
	if err != nil {
		return nil, err
	}
	states := map[string]AppliedState{}
	for _, instanceUuid := range instanceUuids {
		agents, err := p.lookup.ListAgent(ctx, &svclookup.ListAgentRequest{InstanceUuid: instanceUuid})
		if err != nil {
			return nil, fmt.Errorf("list the agents of instance %s: %w", instanceUuid, err)
		}
		for _, agent := range agents.GetAgents() {
			// a provisioner applies nothing on a data plane
			if agent.GetMode() == agentapi.Agent_PROVISIONER {
				continue
			}
			resp, err := p.lookup.ListStatus(ctx, &svclookup.ListStatusRequest{
				InstanceUuid: instanceUuid,
				AgentUuid:    agent.GetUUID(),
				TypeUrl:      p.typeUrl,
			})
			if err != nil {
				return nil, fmt.Errorf("list the statuses of agent %s: %w", agent.GetUUID(), err)
			}
			for _, status := range resp.GetStatus() {
				addStatus(states, status)
			}
		}
	}
	for uuid, state := range states {
		slices.Sort(state.Errors)
		states[uuid] = state
	}
	return states, nil
}

func (p *AgentStatusPoller) listInstances(ctx context.Context) ([]string, error) {
	var uuids []string
	for offset := 0; ; offset += instancesPageSize {
		resp, err := p.instances.ListInstance(ctx, &svcinstance.ListInstanceRequest{
			Limit:  instancesPageSize,
			Offset: int32(offset),
		})
		if err != nil {
			return nil, fmt.Errorf("list the instances: %w", err)
		}
		for _, instance := range resp.GetInstances() {
			uuids = append(uuids, instance.GetUUID())
		}
		if len(resp.GetInstances()) < instancesPageSize {
			return uuids, nil
		}
	}
}

// addStatus merges the status one agent gives into the state of its resource, the worst state winning.
func addStatus(states map[string]AppliedState, status *svclookup.StatusResponse) {
	uuid := statusResourceUuid(status)
	isKnown := status.GetState() != svclookup.StatusResponse_UNKNOWN
	if uuid == "" || !isKnown {
		return
	}
	state, seen := states[uuid]
	if !seen || severity(status.GetState()) > severity(state.State) {
		state.State = status.GetState()
	}
	if status.GetState() == svclookup.StatusResponse_ERRORED {
		state.Errors = append(state.Errors, fmt.Sprintf("%s: %s", status.GetAgentUuid(), status.GetError()))
	}
	states[uuid] = state
}

// statusResourceUuid reads the uuid of the resource from the version the status carries, the errored one first since
// it is the version the agent works on.
func statusResourceUuid(status *svclookup.StatusResponse) string {
	resource := status.GetErroredResource()
	if resource == nil {
		resource = status.GetPreviousResource()
	}
	if resource == nil {
		return ""
	}
	message, err := resource.UnmarshalNew()
	if err != nil {
		return ""
	}
	identified, ok := message.(interface{ GetUUID() string })
	if !ok {
		return ""
	}
	return identified.GetUUID()
}

func severity(state svclookup.StatusResponse_State) int {
	switch state {
	case svclookup.StatusResponse_ERRORED:
		return 3
	case svclookup.StatusResponse_ADDING, svclookup.StatusResponse_REMOVING:
		return 2
	case svclookup.StatusResponse_OK:
		return 1
	default:
		return 0
	}
}

func sameState(a, b AppliedState) bool {
	return a.State == b.State && slices.Equal(a.Errors, b.Errors)
}
