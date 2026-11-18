package agentserv

import (
	"crypto/x509"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/storers"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/agent/starling/agentlist"
	"github.com/seagalia/flock/sdk/agentsdk/loghooks"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	"github.com/seagalia/flock/sdk/flock/services/agent/manage/v1/agentmgmtconnect"
	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1/agentstatusconnect"
	"github.com/seagalia/flock/sdk/logging"
)

type backendEndpointStats interface {
	EndpointStatsByBackends() []*backend.BackendEndpointStats
	EndpointStatsForBackend(backendUuid string) *backend.BackendEndpointStats
}
type TlsInfo struct {
	RootCaPool   *x509.CertPool
	CAFilePath   string
	CertFilePath string
	KeyFilePath  string
}
type AgentServer struct {
	entry          zerolog.Logger
	stateStore     *stores.StateStore
	prevStore      storers.StoreManager
	secretLinks    *stores.SecretLinks
	vips           storers.Storer[*vip.VIP]
	logListHook    *loghooks.ListenerWriter
	edpStats       backendEndpointStats
	tlsInfo        TlsInfo
	isEnvoyAlive   func() error
	isWafAlive     func() error
	name           string
	vipPlacer      *agentlist.VipPlacer
	compStatusDesc *prometheus.Desc
	statusDesc     *prometheus.Desc
	announceDesc   *prometheus.Desc
	agentstatusconnect.UnimplementedResourceStatusHandler
	agentstatusconnect.UnimplementedAgentStatusHandler
	agentlogsconnect.UnimplementedLogsHandler
	agentmgmtconnect.UnimplementedManageHandler
}

func NewAgentServer(
	name string,
	edpStats backendEndpointStats,
	logListHook *loghooks.ListenerWriter,
	tlsInfo TlsInfo,
	isEnvoyAlive func() error,
	isWafAlive func() error,
	vipPlacer *agentlist.VipPlacer,
	// vips are the ones the agent holds, prevStore the resources as the controller sent them
	vips storers.Storer[*vip.VIP],
	prevStore storers.StoreManager,
	stateStore *stores.StateStore,
	secretLinks *stores.SecretLinks,
) *AgentServer {
	return &AgentServer{
		name:         name,
		entry:        logging.Component("agent_server"),
		stateStore:   stateStore,
		prevStore:    prevStore,
		secretLinks:  secretLinks,
		vips:         vips,
		edpStats:     edpStats,
		logListHook:  logListHook,
		tlsInfo:      tlsInfo,
		isEnvoyAlive: isEnvoyAlive,
		isWafAlive:   isWafAlive,
		vipPlacer:    vipPlacer,
		statusDesc: prometheus.NewDesc(
			"flock_agent_status",
			"Agent status",
			[]string{"name"},
			nil,
		),
		compStatusDesc: prometheus.NewDesc(
			"flock_agent_status_components",
			"Components status",
			[]string{"name"},
			nil,
		),
		announceDesc: prometheus.NewDesc(
			"flock_agent_announce_vips",
			"Announcing vips",
			[]string{"uuid", "ip", "prefix"},
			nil,
		),
	}
}
