package app

import (
	"context"
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"sync"
	"time"

	"errors"

	"github.com/ArthurHlt/go-syslog-client"
	"github.com/KimMachineGun/automemlimit/memlimit"
	"github.com/Masterminds/semver/v3"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/seagalia/flock/agent/starling/agentlist"
	"github.com/seagalia/flock/agent/starling/backendctl"
	"github.com/seagalia/flock/agent/starling/bgp"
	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/agent/starling/envoyctl"
	"github.com/seagalia/flock/agent/starling/handlers"
	"github.com/seagalia/flock/agent/starling/ipvsctl"
	"github.com/seagalia/flock/agent/starling/layer2"
	"github.com/seagalia/flock/agent/starling/memsplit"
	"github.com/seagalia/flock/agent/starling/nm"
	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
	"github.com/seagalia/flock/sdk/agentsdk/aggregaters"
	"github.com/seagalia/flock/sdk/agentsdk/loghooks"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/metrics"
)

const (
	DefaultBufCapAdsHdl = 20
	// stopBudget bounds the whole stop, the withdrawal of the announces included
	stopBudget = 10 * time.Minute
)

// errPartFailed ends Run after a handler or server failure, the agent then exits with an error
var errPartFailed = errors.New("a handler or server failed")

// reportFailure asks the agent to stop. One failure is enough and nobody reads the channel once the stop began, so a
// failure finding it full is dropped instead of blocking its goroutine, which the stop waits for.
func reportFailure(hasError chan<- struct{}) {
	select {
	case hasError <- struct{}{}:
	default:
	}
}

type garbageCollected interface {
	GarbageCollect() error
}

type App struct {
	name          string
	version       *semver.Version
	entry         zerolog.Logger
	cnf           *config.Config
	rt            *runtime.Runtime
	aggregator    *aggregaters.Aggregator
	tlsConf       *tls.Config
	netman        nm.NetworkManager
	ipWatcher     machineIPsWatcher
	ipvsWildcards *ipvsctl.WildcardClient
	jointer       *backendctl.Joint
	configWatcher *envoyctl.ConfigWatcher
	ctx           context.Context
	cancelFunc    context.CancelFunc
	// announceCtx ends before ctx at the stop, the BGP server, the L2 announcer and the agent list run on it
	announceCtx      context.Context
	cancelAnnounce   context.CancelFunc
	garbageCollected []garbageCollected
	l2Announcer      *layer2.Announce
	node             *core.Node
	listenerHook     *loghooks.ListenerWriter
	bgpServer        *bgp.Server
	adHandler        handlers.AttachDetachHandler
	isEnvoyAlive     func() error
	isWafAlive       func() error
	agentList        *agentlist.AgentList
	vipPlacer        *agentlist.VipPlacer
	runEnvoy         bool
	cleanOnCtxDone   bool
	memory           *memsplit.Split
}

func NewApp(cnf *config.Config, version *semver.Version) (*App, error) {
	metrics.EnableAppMetrics(config.AppName)
	metrics.EnableAppMetrics(metrics.AgentSDKAppName)
	name, err := agentconfig.CommonName(cnf.TlsConfig.CertFile)
	if err != nil {
		return nil, err
	}
	ctx, cancelFunc := context.WithCancel(context.Background())
	announceCtx, cancelAnnounce := context.WithCancel(ctx)
	app := &App{
		name:             name,
		entry:            logging.Component("app"),
		cnf:              cnf,
		rt:               runtime.New(),
		ctx:              ctx,
		cancelFunc:       cancelFunc,
		announceCtx:      announceCtx,
		cancelAnnounce:   cancelAnnounce,
		garbageCollected: make([]garbageCollected, 0),
		version:          version,
		isEnvoyAlive: func() error {
			return nil
		},
		isWafAlive: func() error {
			return nil
		},
	}

	err = app.setupLogging()
	if err != nil {
		return nil, fmt.Errorf("setup logging: %w", err)
	}
	stores.CountResourcesOf(app.rt.Store)

	err = app.preInitIpvs()
	if err != nil {
		return nil, fmt.Errorf("pre init ipvs: %w", err)
	}

	err = app.preInitOS()
	if err != nil {
		return nil, fmt.Errorf("pre init os: %w", err)
	}

	err = app.preInit()
	if err != nil {
		return nil, fmt.Errorf("app preInit: %w", err)
	}

	err = app.loadIPWatcher()
	if err != nil {
		return nil, fmt.Errorf("load address source: %w", err)
	}

	app.loadConfigWatcher()

	app.loadBgpServer()

	err = app.loadTlsConfig()
	if err != nil {
		return nil, fmt.Errorf("load tls config: %w", err)
	}

	app.loadAggregator()

	err = app.loadAdvertisement()
	if err != nil {
		return nil, fmt.Errorf("load advertisement: %w", err)
	}

	app.loadJointer()

	app.loadBackendHandler()

	// as vs handler is the ad handler too needed by vip handler it should be load before
	err = app.loadVsHandler()
	if err != nil {
		return nil, fmt.Errorf("load virtual server handler: %w", err)
	}

	app.loadVipHandler()

	app.loadCertificateHandler()

	app.loadSecretHandler()

	return app, nil
}

// nolint:unused
func (a *App) postInit() error {
	return nil
}

func (a *App) loadBgpServer() {
	if a.cnf.AdvertisementConfig.BgpConfig == nil {
		return
	}
	bgpConf := a.cnf.AdvertisementConfig.BgpConfig
	a.bgpServer = bgp.NewServer(
		bgpConf.LocalAsn,
		bgpConf.RouterId,
		bgpConf.Peers,
		bgpConf.Advertisements,
	)
}

func (a *App) preInit() error {
	a.memory = memsplit.New(a.cnf.WafConfig, a.cnf.DevMode)
	// under the memory.high of the agent's cgroup (95%), so the Go collector works harder before the kernel reclaims,
	// and the only hold on the agent's share without the split
	_, err := memlimit.Set(memlimit.WithProvider(memlimit.Limit(a.memory.Agent)), memlimit.WithRatio(0.9))
	if err != nil {
		return fmt.Errorf("set the go memory limit: %w", err)
	}
	a.node = &core.Node{
		ClientType: "starling",
		ClientVersion: &core.SemanticVersion{
			Major:      uint32(a.version.Major()),
			Minor:      uint32(a.version.Minor()),
			Patch:      uint32(a.version.Patch()),
			Prerelease: a.version.Prerelease(),
			Metadata:   a.version.Metadata(),
		},
		Extensions: extensionsLoaded,
	}
	a.entry.Debug().Msg("Cleaning previous nftables table and vips ...")
	err = a.cleanHostNetwork()
	if err != nil {
		a.entry.Warn().Err(err).Msg("Failed to clean previous nftables table and vips")
	}
	a.entry.Debug().Msg("Finished cleaning previous nftables table and vips ...")

	if len(a.cnf.Plugin.HealthChecks) > 0 {
		backendctl.SetPluginsHc(a.cnf.Plugin.HealthChecks)
	}

	isWafInAgent := a.cnf.WafConfig.WafEmbedded || a.cnf.WafConfig.WafExec != nil
	if a.cnf.EnvoyConfig.EnvoyService != nil && isWafInAgent {
		a.entry.Warn().Msg("Envoy runs as a service and outlives the agent, but the WAF stops with the agent, so the " +
			"routes with a WAF fail while the agent restarts (or pass uninspected with fail_open). " +
			"Run the WAF as a service too.")
	}

	a.entry.Info().Str("name", a.name).Msg("Starting flock-agent ...")
	return nil
}

// cleanHostNetwork deletes the nftables table and flock0 with its vips. Dev mode runs on a laptop, so it leaves the
// network of its host alone, like its network manager does.
func (a *App) cleanHostNetwork() error {
	if a.cnf.DevMode {
		return nil
	}
	return nm.Cleanup(*a.cnf.AdvertisementConfig.NetworkManager)
}

func (a *App) preInitIpvs() error {
	a.runEnvoy = a.decideRunEnvoy()
	if !a.cnf.DevMode {
		return ipvsctl.Init()
	}
	log.Warn().Msg("dev mode is enabled:")
	if a.runEnvoy {
		log.Warn().Msgf(" - envoy is started (%s)", a.cnf.EnvoyConfig.EnvoyExec.BinPath)
		log.Warn().Msg(" - IPVS is not used: TCP and UDP virtual servers are served by envoy, like HTTP and TLS ones")
	} else {
		log.Warn().Msg(" - envoy binary not found (or envoy.service configured): no envoy will be started")
		log.Warn().Msg(" - Ipvs is disabled but faked and logged")
	}
	log.Warn().Msg(" - network manager is disabled but logged")

	ipvsctl.SetClient(ipvsctl.NewFakeIpvsClient())
	return nil
}

// decideRunEnvoy tells if envoy runs: always outside dev mode, in dev mode only when the exec binary is found
func (a *App) decideRunEnvoy() bool {
	if !a.cnf.DevMode {
		return true
	}
	// dev mode only runs envoy as a child process, never a system service
	if a.cnf.EnvoyConfig.EnvoyExec == nil {
		return false
	}
	_, err := exec.LookPath(a.cnf.EnvoyConfig.EnvoyExec.BinPath)
	return err == nil
}

// setupLogging runs first so every later step logs to the configured outputs
func (a *App) setupLogging() error {
	a.listenerHook = loghooks.NewListenerWriter()
	opts := logging.Options{
		Level:            a.cnf.Log.Level,
		NoColor:          a.cnf.Log.NoColor,
		ForceColors:      a.cnf.Log.ForceColors,
		InJson:           a.cnf.Log.InJson,
		FilterComponents: a.cnf.Log.FilterComponents,
		Listener:         a.listenerHook,
	}
	if a.cnf.Log.Syslog != nil {
		syslogWriter, err := syslog.NewWriter(a.cnf.Log.Syslog.Targets...)
		if err != nil {
			return fmt.Errorf("create syslog writer: %w", err)
		}
		opts.Syslog = loghooks.NewSyslogWriter(
			syslogWriter,
			fmt.Sprintf("%s.%s.agent", a.name, a.cnf.ControllerConfig.InstanceUuid),
			"flock-agent",
			a.cnf.Log.Syslog.FormatRfc3164,
			a.cnf.Log.Syslog.StructuredData,
		)
	}
	return logging.Setup(opts)
}

func (a *App) loadConfigWatcher() {
	disableSnat := false
	if a.cnf.AdvertisementConfig != nil {
		disableSnat = a.cnf.AdvertisementConfig.DisableSnat
	}
	a.configWatcher = envoyctl.NewConfigWatcher(a.ctx, disableSnat, a.rt.Store, a.rt.States)
}

func (a *App) onUsr2Signal() {
	if a.agentList != nil {
		a.agentList.ResetStartTime()
	}
}

// Run runs the agent until ctx ends, one of its parts fails or the clean signal comes, then stops it in order
func (a *App) Run(ctx context.Context) error {
	a.entry.Info().Str("name", a.name).Msg("Starting node ...")
	wg := &sync.WaitGroup{}
	announceWg := &sync.WaitGroup{}

	hasError := make(chan struct{}, 1)

	// Start first bgp server to ensure that routing is working before all
	a.RunBgpServer(announceWg, hasError)

	a.RunAnnouncers(announceWg)

	a.RunL3Log(wg, hasError)

	a.RunIPWatcher(wg, hasError)

	a.StartHandlers(wg, hasError)

	a.configWatcher.RunRequestQueues()

	a.RunWaf(wg, hasError)

	if a.runEnvoy {
		a.RunEnvoy(wg, hasError)
	} else {
		a.RunConfWatcherConsumer(wg, hasError)
	}

	a.RunServer(wg, hasError)
	a.startGarbage()

	shouldClean, failure := a.waitStopAsked(ctx, hasError)
	return stderrors.Join(failure, a.stop(announceWg, wg, shouldClean))
}

// CleanOnCtxDone makes the end of the ctx given to Run also clean the host (nftables table, vips, IPVS services), as
// SIGUSR1 does, for an embedder that can't send a signal.
func (a *App) CleanOnCtxDone() {
	a.cleanOnCtxDone = true
}

// waitStopAsked returns once the agent must stop, telling if it must also clean what it set up on its host. A failure
// is returned so the process exits with an error and its supervisor sees it.
func (a *App) waitStopAsked(ctx context.Context, hasError <-chan struct{}) (bool, error) {
	sig := make(chan os.Signal, 1)
	// signal.Notify with no signal relays them all, and windows has no user signal
	if len(userSignals) > 0 {
		signal.Notify(sig, userSignals...)
		defer signal.Stop(sig)
	}
	for {
		select {
		case <-ctx.Done():
			a.entry.Info().Msg("Stop asked")
			return a.cleanOnCtxDone, nil
		case <-hasError:
			a.entry.Warn().Msg("one handler or server failed")
			return false, errPartFailed
		case s := <-sig:
			if s == resetStartSignal {
				a.onUsr2Signal()
				continue
			}
			a.entry.Info().Msgf("Signal (%v) received, stopping and cleaning", s)
			return true, nil
		}
	}
}

// stop withdraws the announces first, so BGP peers, neighbours and the other agents move the traffic of the vips away
// while envoy still serves it here. Only then the app ctx ends, which stops envoy, the WAF, the handlers and the
// servers.
func (a *App) stop(announceWg, wg *sync.WaitGroup, shouldClean bool) error {
	deadline := time.Now().Add(stopBudget)
	a.entry.Info().Msg("Withdrawing the announces ...")
	a.cancelAnnounce()
	// gobgp's StopBgp doesn't watch its ctx, the budget is the only bound of a stuck BGP stop. The rest still stops.
	err := waitStopped(announceWg, time.Until(deadline))
	if err != nil {
		a.entry.Error().Err(err).Msg("Announces not withdrawn in time, stopping the rest")
	} else {
		a.entry.Info().Msg("Announces withdrawn")
	}

	// before waiting for the handlers, which can't end while their stream opening or its first send runs
	err = a.aggregator.Close()
	if err != nil {
		a.entry.Error().Err(err).Msg("close aggregator failed")
	}

	a.entry.Info().Msg("Gracefully stopping all handlers and servers ...")
	if shouldClean {
		for _, h := range a.rt.Handlers("") {
			a.entry.Info().Str("ads_handler_type", reflect.TypeOf(h).String()).Msg("cleaning handler")
			h.SetCleanWhenDone(true)
		}
	}
	a.cancelFunc()
	err = waitStopped(wg, time.Until(deadline))
	if err != nil {
		// a budget used up, by the announce wait too, skips the host clean of SIGUSR1, since parts may still use the host
		return err
	}
	a.entry.Info().Msg("All handlers and server stopped gracefully")
	if shouldClean {
		if err := a.cleanHostNetwork(); err != nil {
			a.entry.Error().Err(err).Msg("failed to clean the nftables table and vips")
		}
	}
	return nil
}

// waitStopped waits for wg within budget. On timeout the agent exits, its waiter goroutine is left behind and closes
// its own channel if wg ever ends, so nothing sends on a channel closed by someone else.
func waitStopped(wg *sync.WaitGroup, budget time.Duration) error {
	stopped := make(chan struct{})
	go func() {
		wg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
		return nil
	case <-time.After(budget):
		return fmt.Errorf("timeout waiting for the stop after %s", budget)
	}
}
