// Package agentserv is the connect server of the F5 agent: resource status with the notes of the approximations
// made on the BIG-IP, agent status with one component per device, logs and server certificate update.
package agentserv

import (
	"context"
	"crypto/x509"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/storers"

	"github.com/rs/zerolog"
	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/agent/f5/bigip"
	"github.com/seagalia/flock/agent/f5/reconciler"
	"github.com/seagalia/flock/sdk/agentsdk/loghooks"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	"github.com/seagalia/flock/sdk/flock/services/agent/manage/v1/agentmgmtconnect"
	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1/agentstatusconnect"
	"github.com/seagalia/flock/sdk/logging"
)

type Reconciler interface {
	Status() reconciler.Status
	PoolsOfBackend(uuid string) []as3.PoolRef
}

type Device interface {
	Devices(ctx context.Context) []bigip.DeviceHealth
	PoolMemberStates(ctx context.Context, tenant, app, pool string) (map[string]bool, error)
}

type TlsInfo struct {
	RootCaPool   *x509.CertPool
	CAFilePath   string
	CertFilePath string
	KeyFilePath  string
}

type AgentServer struct {
	entry       zerolog.Logger
	name        string
	rec         Reconciler
	dev         Device
	logListHook *loghooks.ListenerWriter
	backends    storers.Storer[*backend.Backend]
	prevStore   storers.StoreManager
	stateStore  *stores.StateStore
	secretLinks *stores.SecretLinks
	tlsInfo     TlsInfo
	agentstatusconnect.UnimplementedResourceStatusHandler
	agentstatusconnect.UnimplementedAgentStatusHandler
	agentlogsconnect.UnimplementedLogsHandler
	agentmgmtconnect.UnimplementedManageHandler
}

// NewAgentServer answers the status of the resources the controller sent, read in prevStore with their state in
// stateStore. secretLinks gives the secret references of the errored ones, backends are the ones the agent holds.
func NewAgentServer(
	name string, rec Reconciler, dev Device, logListHook *loghooks.ListenerWriter,
	backends storers.Storer[*backend.Backend], prevStore storers.StoreManager, stateStore *stores.StateStore,
	secretLinks *stores.SecretLinks, tlsInfo TlsInfo,
) *AgentServer {
	return &AgentServer{
		entry:       logging.Component("agent_server"),
		name:        name,
		rec:         rec,
		dev:         dev,
		logListHook: logListHook,
		backends:    backends,
		prevStore:   prevStore,
		stateStore:  stateStore,
		secretLinks: secretLinks,
		tlsInfo:     tlsInfo,
	}
}
