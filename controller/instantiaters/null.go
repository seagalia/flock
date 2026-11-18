package instantiaters

import (
	"context"

	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
)

type Null struct {
}

func NewNull() *Null {
	return &Null{}
}

func (n Null) ListAgents(_ context.Context, instanceUuid string) ([]*agent.Agent, error) {
	return []*agent.Agent{}, nil
}

func (n Null) CanInstantiate(_ context.Context, params InstanceParams) (bool, error) {
	return true, nil
}

func (n Null) CreateServiceInstance(_ context.Context, params InstanceParams) (*instance.InstanceStatus, error) {
	return &instance.InstanceStatus{
		CurrentState: instance.InstanceStatus_RUNNING,
		DesiredState: instance.InstanceStatus_RUNNING,
		ErrorMessage: "",
	}, nil
}

func (n Null) UpdateServiceInstance(_ context.Context, params InstanceParams) (*instance.InstanceStatus, error) {
	return &instance.InstanceStatus{
		CurrentState: instance.InstanceStatus_RUNNING,
		DesiredState: instance.InstanceStatus_RUNNING,
		ErrorMessage: "",
	}, nil
}

func (n Null) DeleteServiceInstance(_ context.Context, instanceUuid string) (*instance.InstanceStatus, error) {
	return &instance.InstanceStatus{
		CurrentState: instance.InstanceStatus_RUNNING,
		DesiredState: instance.InstanceStatus_RUNNING,
		ErrorMessage: "",
	}, nil
}

func (n Null) GetStatus(_ context.Context, instanceUuid string) (*instance.InstanceStatus, error) {
	return &instance.InstanceStatus{
		CurrentState: instance.InstanceStatus_RUNNING,
		DesiredState: instance.InstanceStatus_RUNNING,
		ErrorMessage: "",
	}, nil
}
