package instantiaters

import (
	"context"

	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
)

type InstanceParams struct {
	InstanceUUID        string
	Zone                string
	ResourceTypeName    string
	OriginatingIdentity string
}

type Instantiater interface {
	ListAgents(ctx context.Context, instanceUuid string) ([]*agent.Agent, error)
	CanInstantiate(ctx context.Context, params InstanceParams) (bool, error)
	CreateServiceInstance(ctx context.Context, params InstanceParams) (*instance.InstanceStatus, error)
	UpdateServiceInstance(ctx context.Context, params InstanceParams) (*instance.InstanceStatus, error)
	DeleteServiceInstance(ctx context.Context, instanceUuid string) (*instance.InstanceStatus, error)
	GetStatus(ctx context.Context, instanceUuid string) (*instance.InstanceStatus, error)
}
