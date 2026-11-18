package agentserv

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/agent/f5/bigip"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
)

var minAS3Version = semver.MustParse(as3.SchemaVersion)

// GetAgentStatus reports the agent as a bridge: one component per BIG-IP device and one for the reconciler. The VIPs
// posted are announced by the active device, the standby one gets them through the config sync.
func (s *AgentServer) GetAgentStatus(ctx context.Context, req *agentstatus.GetAgentStatusRequest) (*agent.AgentStatus, error) {
	bridge := &agent.BridgeAgentStatus{}
	var active string
	for _, device := range s.dev.Devices(ctx) {
		state := deviceState(device)
		bridge.ComponentStates = append(bridge.ComponentStates, state)
		if device.Active && active == "" {
			active = state.GetName()
		}
	}
	recStatus := s.rec.Status()
	bridge.ComponentStates = append(bridge.ComponentStates, reconcilerState(recStatus.FailedTenants))
	if req.GetShowAnnouncingVips() && active != "" {
		bridge.ComponentAnnouncingVips = []*agent.ComponentAnnouncingVips{{Name: active, AnnouncingVips: recStatus.PostedVIPs}}
	}
	return &agent.AgentStatus{State: &agent.AgentStatus_ProxyAgentState{ProxyAgentState: bridge}}, nil
}

func deviceState(device bigip.DeviceHealth) *agent.State {
	state := &agent.State{Name: "bigip " + device.URL, Status: agent.Status_OK}
	if !device.Reachable {
		state.Status = agent.Status_ERRORED
		state.Details = fmt.Sprintf("unreachable: %v", device.Err)
		return state
	}
	var problems []string
	if !device.Active {
		problems = append(problems, device.FailoverStatus)
	}
	version, err := semver.NewVersion(device.AS3Version)
	switch {
	case err != nil:
		problems = append(problems, fmt.Sprintf("AS3 version unknown: %v", cmpErr(device.Err, err)))
	case version.LessThan(minAS3Version):
		problems = append(problems, fmt.Sprintf("AS3 %s is older than %s", device.AS3Version, as3.SchemaVersion))
	}
	if len(problems) > 0 {
		state.Status = agent.Status_WARNING
		state.Details = strings.Join(problems, ", ")
	}
	return state
}

// cmpErr prefers the error reading the AS3 info to the one parsing an empty version.
func cmpErr(readErr, parseErr error) error {
	if readErr != nil {
		return readErr
	}
	return parseErr
}

func reconcilerState(failedTenants map[string]string) *agent.State {
	state := &agent.State{Name: "reconciler", Status: agent.Status_OK}
	if len(failedTenants) == 0 {
		return state
	}
	var details []string
	for _, tenant := range slices.Sorted(maps.Keys(failedTenants)) {
		details = append(details, fmt.Sprintf("tenant %s: %s", tenant, failedTenants[tenant]))
	}
	state.Status = agent.Status_ERRORED
	state.Details = strings.Join(details, "; ")
	return state
}
