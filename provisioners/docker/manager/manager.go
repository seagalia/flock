// Package manager keeps the controller in line with the labelled containers of a docker host: on each docker event,
// debounced, it lists the running containers, gives their endpoints to the eds server and writes to the controller
// the backends, certificates and virtual servers that differ.
package manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/provisioners/docker/eds"
	"github.com/seagalia/flock/provisioners/docker/models"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/logging"
)

const (
	defaultQuietDelay       = 200 * time.Millisecond
	defaultMaxDelay         = 2 * time.Second
	defaultResyncInterval   = 5 * time.Minute
	defaultMaxReconnectWait = time.Minute
	defaultMinReconnectWait = time.Second
)

// the delays are vars only so specs don't wait the real ones
var (
	// quietDelay is the silence a burst of events must end with before a rebuild, a compose up sends dozens
	quietDelay = defaultQuietDelay
	// maxDelay bounds how stale the controller gets while events never stop
	maxDelay = defaultMaxDelay
	// resyncInterval brings back what someone changed on the controller side, no docker event tells it
	resyncInterval   = defaultResyncInterval
	minReconnectWait = defaultMinReconnectWait
	maxReconnectWait = defaultMaxReconnectWait
)

type dockerClient interface {
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	Events(ctx context.Context, options client.EventsListOptions) client.EventsResult
}

// AgentIdentity gives the uuid the controller knows the provisioner by, the aggregator learns it on the first stream
type AgentIdentity interface {
	WaitAgentUuid(ctx context.Context) (string, error)
}

type ControllerClients struct {
	Backends svcbackendconnect.BackendClient
	Certs    svccertconnect.CertificateClient
	Vss      svcvsconnect.VirtualServerClient
	Vips     svcvipconnect.VIPClient
}

type Manager struct {
	entry     zerolog.Logger
	cnf       *config.Config
	docker    dockerClient
	edsServer *eds.Server
	clients   ControllerClients
	identity  AgentIdentity
	// agentUuid and converter are set by Run once the controller gave the uuid
	agentUuid string
	converter *models.Converter
	// owned caches what the provisioner wrote on the controller, nil when it must be listed again. Only the watch loop
	// touches it.
	owned *owned
}

func NewManager(
	cnf *config.Config, docker dockerClient, edsServer *eds.Server, clients ControllerClients, identity AgentIdentity,
) *Manager {
	return &Manager{
		entry:     logging.Component("docker"),
		cnf:       cnf,
		docker:    docker,
		edsServer: edsServer,
		clients:   clients,
		identity:  identity,
	}
}

// Run follows the docker events until ctx ends, reconnecting to docker after a failure.
func (m *Manager) Run(ctx context.Context) error {
	agentUuid, err := m.identity.WaitAgentUuid(ctx)
	if err != nil {
		return fmt.Errorf("wait the uuid of the provisioner: %w", err)
	}
	m.converter, err = models.NewConverter(m.cnf, agentUuid)
	if err != nil {
		return err
	}
	m.agentUuid = agentUuid

	reconnectWait := time.Duration(0)
	for {
		watchStart := time.Now()
		err := m.watch(ctx)
		if ctx.Err() != nil {
			return nil
		}
		// a watch that ran a while is a new failure, not the same one again
		if time.Since(watchStart) > maxReconnectWait {
			reconnectWait = 0
		}
		reconnectWait = min(max(reconnectWait*2, minReconnectWait), maxReconnectWait)
		m.entry.Error().Err(err).Msgf("docker events failed, reconnecting in %s", reconnectWait)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(reconnectWait):
		}
	}
}

// watch rebuilds at once, since events may have been missed before the subscription, then after each burst of events
// once it quieted down, at least every maxDelay while they keep coming, and every resyncInterval. The first rebuild
// and the resyncs list the owned resources again, the others trust the cache.
func (m *Manager) watch(ctx context.Context) error {
	eventsCtx, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()
	dockerEvents := m.docker.Events(eventsCtx, client.EventsListOptions{
		Filters: make(client.Filters).Add("type", string(events.ContainerEventType)),
	})
	m.owned = nil
	m.rebuild(ctx)

	quietTimer := time.NewTimer(quietDelay)
	quietTimer.Stop()
	maxTimer := time.NewTimer(maxDelay)
	maxTimer.Stop()
	resyncTicker := time.NewTicker(resyncInterval)
	defer resyncTicker.Stop()
	isDirty := false
	flush := func() {
		quietTimer.Stop()
		maxTimer.Stop()
		if !isDirty {
			return
		}
		isDirty = false
		m.rebuild(ctx)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case _, isOpen := <-dockerEvents.Messages:
			if !isOpen {
				flush()
				return errors.New("the docker events stream ended")
			}
			if !isDirty {
				isDirty = true
				maxTimer.Reset(maxDelay)
			}
			quietTimer.Reset(quietDelay)
		case <-quietTimer.C:
			flush()
		case <-maxTimer.C:
			flush()
		case <-resyncTicker.C:
			m.owned = nil
			isDirty = true
			flush()
		case err := <-dockerEvents.Err:
			flush()
			return fmt.Errorf("docker events: %w", err)
		}
	}
}
