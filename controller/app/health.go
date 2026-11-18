package app

import (
	"context"
	"time"

	"connectrpc.com/grpchealth/v2"
	"github.com/rs/zerolog"

	"github.com/seagalia/flock/sdk/logging"
)

// healthCheckTimeout bounds a whole check, a probe must get its answer before its own timeout
const healthCheckTimeout = 3 * time.Second

type pinger interface {
	Ping(ctx context.Context) error
}

type dependency struct {
	name   string
	pinger pinger
}

// healthChecker reports the controller serving while every dependency it can't work without answers: the
// database, and nats when controllers share their changes through it.
type healthChecker struct {
	entry        zerolog.Logger
	dependencies []dependency
}

func newHealthChecker(dependencies ...dependency) *healthChecker {
	return &healthChecker{
		entry:        logging.Component("health"),
		dependencies: dependencies,
	}
}

// Check answers NOT_SERVING when a dependency fails, its error is only logged: the health endpoint is
// unauthenticated and the errors of a database may name its host and user.
func (c *healthChecker) Check(ctx context.Context, _ *grpchealth.CheckRequest) (*grpchealth.CheckResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()
	status := grpchealth.StatusServing
	for _, dep := range c.dependencies {
		err := dep.pinger.Ping(ctx)
		if err == nil {
			continue
		}
		c.entry.Error().Str("dependency", dep.name).Msgf("health check failed: %v", err)
		status = grpchealth.StatusNotServing
	}
	return &grpchealth.CheckResponse{Status: status}, nil
}
