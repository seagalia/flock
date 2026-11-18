// Package agentserv is the connect server the controller calls on the provisioner: status of the certificates it
// provisions, agent status with one component per provisioner kind, and logs.
package agentserv

import (
	"github.com/seagalia/flock/sdk/agentsdk/loghooks"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1/agentstatusconnect"
	"github.com/seagalia/flock/sdk/storers"
)

type Server struct {
	name          string
	isAcmeEnabled bool
	prevStore     storers.StoreManager
	stateStore    *stores.StateStore
	secretLinks   *stores.SecretLinks
	logListHook   *loghooks.ListenerWriter
	agentstatusconnect.UnimplementedResourceStatusHandler
	agentstatusconnect.UnimplementedAgentStatusHandler
	agentlogsconnect.UnimplementedLogsHandler
}

// NewServer answers the status of the resources the controller sent, read in prevStore with their state in stateStore.
// secretLinks gives the secret references of the errored ones.
func NewServer(
	name string, prevStore storers.StoreManager, stateStore *stores.StateStore, secretLinks *stores.SecretLinks,
	logHook *loghooks.ListenerWriter, isAcmeEnabled bool,
) *Server {
	return &Server{
		name:          name,
		isAcmeEnabled: isAcmeEnabled,
		prevStore:     prevStore,
		stateStore:    stateStore,
		secretLinks:   secretLinks,
		logListHook:   logHook,
	}
}
