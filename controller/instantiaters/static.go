package instantiaters

import (
	"context"

	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
)

type Static struct {
	agents []*agent.Agent
}

func NewStatic(agentInst []*config.AgentInstantiator) *Static {
	agents := make([]*agent.Agent, len(agentInst))
	for i, inst := range agentInst {
		agents[i] = inst.ToAgent()
	}
	return &Static{agents: agents}
}

func (s *Static) ListAgents(_ context.Context, instanceUuid string) ([]*agent.Agent, error) {
	return s.agents, nil
}

func (s *Static) CanInstantiate(_ context.Context, params InstanceParams) (bool, error) {
	return true, nil
}

func (s *Static) CreateServiceInstance(_ context.Context, params InstanceParams) (*instance.InstanceStatus, error) {
	return &instance.InstanceStatus{
		CurrentState: instance.InstanceStatus_RUNNING,
		DesiredState: instance.InstanceStatus_RUNNING,
		ErrorMessage: "",
	}, nil
}

func (s *Static) UpdateServiceInstance(_ context.Context, params InstanceParams) (*instance.InstanceStatus, error) {
	return &instance.InstanceStatus{
		CurrentState: instance.InstanceStatus_RUNNING,
		DesiredState: instance.InstanceStatus_RUNNING,
		ErrorMessage: "",
	}, nil
}

func (s *Static) DeleteServiceInstance(_ context.Context, instanceUuid string) (*instance.InstanceStatus, error) {
	return &instance.InstanceStatus{
		CurrentState: instance.InstanceStatus_RUNNING,
		DesiredState: instance.InstanceStatus_RUNNING,
		ErrorMessage: "",
	}, nil
}

func (s *Static) GetStatus(_ context.Context, instanceUuid string) (*instance.InstanceStatus, error) {
	return &instance.InstanceStatus{
		CurrentState: instance.InstanceStatus_RUNNING,
		DesiredState: instance.InstanceStatus_RUNNING,
		ErrorMessage: "",
	}, nil
}
