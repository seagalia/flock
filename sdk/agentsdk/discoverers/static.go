package discoverers

import (
	"context"

	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
)

type StaticDiscovery struct {
	targets []*core.IPAddr
}

func NewStatic(targets []*core.IPAddr) *StaticDiscovery {
	return &StaticDiscovery{targets: targets}
}

func (s StaticDiscovery) Run(ctx context.Context, found chan<- []*core.IPAddr) error {
	select {
	case found <- s.targets:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
