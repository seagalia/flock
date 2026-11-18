// Package app wires the F5 agent: the resources of the controller go to the handlers, the reconciler turns them into
// AS3 tenants posted to the BIG-IP, and the connect server reports their state.
package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ArthurHlt/go-syslog-client"
	"github.com/Masterminds/semver/v3"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/metrics"

	"github.com/seagalia/flock/agent/f5/bigip"
	"github.com/seagalia/flock/agent/f5/config"
	"github.com/seagalia/flock/agent/f5/handlers"
	"github.com/seagalia/flock/agent/f5/reconciler"
	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
	"github.com/seagalia/flock/sdk/agentsdk/aggregaters"
	"github.com/seagalia/flock/sdk/agentsdk/certman"
	"github.com/seagalia/flock/sdk/agentsdk/loghooks"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/logging"
)

const (
	adsChanCapacity = 20
	// stopMargin is what the stop waits on top of the device request timeout, for the requests in flight to end
	stopMargin = time.Minute
)

// errPartFailed ends Run after a handler or server failure, the agent then exits with an error
var errPartFailed = errors.New("a handler or server failed")

type App struct {
	// name is the CN of the agent certificate, the identity the controller knows the agent by
	name         string
	cnf          *config.Config
	version      *semver.Version
	entry        zerolog.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	listenerHook *loghooks.ListenerWriter
	rt           *runtime.Runtime
	tlsConf      *tls.Config
	certman      *certman.CertMan
	node         *core.Node
	aggregator   *aggregaters.Aggregator
	device       *bigip.Client
	rec          *reconciler.Reconciler
	backends     *handlers.BackendHandler
}

func NewApp(cnf *config.Config, version *semver.Version) (*App, error) {
	metrics.EnableAppMetrics(config.AppName)
	metrics.EnableAppMetrics(metrics.AgentSDKAppName)
	name, err := agentconfig.CommonName(cnf.TlsConfig.CertFile)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	app := &App{
		name:    name,
		cnf:     cnf,
		version: version,
		entry:   logging.Component("app"),
		ctx:     ctx,
		cancel:  cancel,
		rt:      runtime.New(),
	}

	err = app.setupLogging()
	if err != nil {
		return nil, fmt.Errorf("setup logging: %w", err)
	}
	stores.CountResourcesOf(app.rt.Store)

	app.preInit()

	err = app.loadTlsConfig()
	if err != nil {
		return nil, fmt.Errorf("load tls config: %w", err)
	}

	err = app.loadDevice()
	if err != nil {
		return nil, fmt.Errorf("load bigip client: %w", err)
	}

	app.loadReconciler()
	app.loadAggregator()

	app.loadCertificateHandler()
	app.loadBackendHandler()
	app.loadVsHandler()
	app.loadVipHandler()
	app.loadSecretHandler()

	return app, nil
}

func (a *App) preInit() {
	a.node = &core.Node{
		ClientType: "f5",
		ClientVersion: &core.SemanticVersion{
			Major:      uint32(a.version.Major()),
			Minor:      uint32(a.version.Minor()),
			Patch:      uint32(a.version.Patch()),
			Prerelease: a.version.Prerelease(),
			Metadata:   a.version.Metadata(),
		},
		Extensions: extensionsLoaded,
	}
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
			"flock-f5-agent",
			a.cnf.Log.Syslog.FormatRfc3164,
			a.cnf.Log.Syslog.StructuredData,
		)
	}
	return logging.Setup(opts)
}

// reportFailure asks the agent to stop. One failure is enough and nobody reads the channel once the stop began, so a
// failure finding it full is dropped instead of blocking its goroutine, which the stop waits for.
func reportFailure(hasError chan<- struct{}) {
	select {
	case hasError <- struct{}{}:
	default:
	}
}

// Run receives the resources and reconciles the device until ctx ends or a part fails. Nothing is removed from the
// device on stop. The tenants stay as they are and the next start compares against them.
func (a *App) Run(ctx context.Context) error {
	a.entry.Info().Str("name", a.name).Strs("bigip", a.cnf.Bigip.URLs).Msg("Starting F5 agent ...")
	a.entry.Warn().Msg("The F5 agent is heavily experimental, do not run it against a production BIG-IP: its behavior and configuration may change in any release")
	wg := &sync.WaitGroup{}
	hasError := make(chan struct{}, 1)

	a.StartHandlers(wg, hasError)
	a.RunReconciler(wg)
	a.RunServer(wg, hasError)

	var failure error
	select {
	case <-ctx.Done():
		a.entry.Info().Msg("Stop asked")
	case <-hasError:
		a.entry.Warn().Msg("one handler or server failed")
		failure = errPartFailed
	}
	return errors.Join(failure, a.stop(wg))
}

// stop ends the app ctx first, so the handlers leave their stream before the aggregator closes the connection under
// them. A request to the device in flight can take up to its timeout, the wait leaves it stopMargin more.
func (a *App) stop(wg *sync.WaitGroup) error {
	a.entry.Info().Msg("Gracefully stopping handlers, reconciler and server ...")
	a.cancel()
	if err := a.aggregator.Close(); err != nil {
		a.entry.Error().Err(err).Msg("close aggregator failed")
	}
	stopped := make(chan struct{})
	go func() {
		wg.Wait()
		// the servers read the certificate from certman until they are stopped, so it stops after them, also when
		// the stop budget below ran out first
		a.certman.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Duration(a.cnf.Bigip.RequestTimeout) + stopMargin):
		return fmt.Errorf("timeout waiting for the handlers and the reconciler to stop")
	}
	a.entry.Info().Msg("F5 agent stopped")
	return nil
}
