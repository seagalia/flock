// Package app wires the provisioners: the resources of the controller go to the handlers of each enabled
// provisioner, which write back to the controller what they provisioned.
package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/seagalia/flock/sdk/metrics"

	"github.com/ArthurHlt/go-syslog-client"
	"github.com/Masterminds/semver/v3"
	"github.com/moby/moby/client"
	"github.com/rs/zerolog"

	"github.com/seagalia/flock/provisioners/acme/certmanagers"
	"github.com/seagalia/flock/provisioners/acme/challenges"
	"github.com/seagalia/flock/provisioners/acme/factories"
	"github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/provisioners/docker/eds"
	"github.com/seagalia/flock/provisioners/docker/manager"
	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
	"github.com/seagalia/flock/sdk/agentsdk/aggregaters"
	"github.com/seagalia/flock/sdk/agentsdk/certman"
	"github.com/seagalia/flock/sdk/agentsdk/leases"
	"github.com/seagalia/flock/sdk/agentsdk/loghooks"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	"github.com/seagalia/flock/sdk/flock/services/lease/v1/svcleaseconnect"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
)

const (
	DefaultBufCapAdsHdl = 20
	stopTimeout         = time.Minute
)

type App struct {
	// name is the CN of the provisioner certificate, the identity the controller knows the provisioner by
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
	grpcClients  *helpers.GrpcClients
	certManager  *certmanagers.CertManager
	// dockerClient, edsServer and dockerManager exist only when the docker block is enabled
	dockerClient  *client.Client
	edsServer     *eds.Server
	dockerManager *manager.Manager
	// challengeTable and the listeners exist only when http-01 or tls-alpn-01 is enabled, a listener only for its own
	// challenge
	challengeTable    *challenges.Table
	http01Listener    net.Listener
	tlsAlpn01Listener net.Listener
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

	// the servers close the listeners once running, until then a failure leaves them to this
	isBuilt := false
	defer func() {
		if !isBuilt {
			app.closeChallengeListeners()
		}
	}()

	err = app.setupLogging()
	if err != nil {
		return nil, fmt.Errorf("setup logging: %w", err)
	}

	app.preInit()

	err = app.loadTlsConfig()
	if err != nil {
		return nil, fmt.Errorf("load tls config: %w", err)
	}

	app.loadAggregator()

	// the provisioners write back to the controller through these clients
	app.grpcClients = helpers.NewGrpcClients(app.tlsConf.Clone())

	err = app.loadCertManager()
	if err != nil {
		return nil, fmt.Errorf("load cert manager: %w", err)
	}

	app.loadCertificateHandler()

	app.loadSecretHandler()

	err = app.loadDockerManager()
	if err != nil {
		return nil, fmt.Errorf("load docker manager: %w", err)
	}

	isBuilt = true
	return app, nil
}

func (a *App) loadCertManager() error {
	acmeConf := a.cnf.Acme
	if !acmeConf.Enabled {
		return nil
	}

	challengesConf := acmeConf.Challenges
	legoFactory, err := factories.NewLegoFactory(
		acmeConf.DefaultCADirUrl, acmeConf.DefaultDnsConfig, challengesConf.Http01Enabled, challengesConf.TlsAlpn01Enabled,
	)
	if err != nil {
		return fmt.Errorf("create lego factory: %w", err)
	}
	legoFactory.SetDns01SkipPropagationCheck(challengesConf.Dns01SkipPropagationCheck)
	controllerClient := a.grpcClients.Client(a.cnf.ControllerConfig.Addr)
	a.certManager = certmanagers.NewCertManager(
		legoFactory,
		svccertconnect.NewCertificateClient(controllerClient),
		svcsecretconnect.NewSecretClient(controllerClient),
		leases.NewClient(svcleaseconnect.NewLeaseClient(controllerClient), a.cnf.ControllerConfig.InstanceUuid),
		a.aggregator,
		a.rt.States,
		acmeConf.DefaultEmail,
	)
	a.certManager.SetLeaseTTL(acmeConf.LeaseTTL)

	hasSharedChallenge := challengesConf.Http01Enabled || challengesConf.TlsAlpn01Enabled
	if !hasSharedChallenge {
		return nil
	}
	a.challengeTable = challenges.NewTable()
	a.certManager.SetChallenges(a.challengeTable)
	return a.listenForChallenges(challengesConf)
}

// listenForChallenges binds the challenge ports at once, so a port already taken fails the start.
func (a *App) listenForChallenges(challengesConf config.Challenges) error {
	var err error
	if challengesConf.Http01Enabled {
		a.http01Listener, err = listenOn(challengesConf.Host, challengesConf.Http01Port)
		if err != nil {
			return fmt.Errorf("listen for http-01: %w", err)
		}
	}
	if challengesConf.TlsAlpn01Enabled {
		a.tlsAlpn01Listener, err = listenOn(challengesConf.Host, challengesConf.TlsAlpn01Port)
		if err != nil {
			return fmt.Errorf("listen for tls-alpn-01: %w", err)
		}
	}
	return nil
}

func listenOn(host string, port uint) (net.Listener, error) {
	return net.Listen("tcp", net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10)))
}

func (a *App) closeChallengeListeners() {
	for _, listener := range []net.Listener{a.http01Listener, a.tlsAlpn01Listener} {
		if listener != nil {
			_ = listener.Close()
		}
	}
}

func (a *App) preInit() {
	a.node = &core.Node{
		ClientType: "provisioner",
		ClientVersion: &core.SemanticVersion{
			Major:      uint32(a.version.Major()),
			Minor:      uint32(a.version.Minor()),
			Patch:      uint32(a.version.Patch()),
			Prerelease: a.version.Prerelease(),
			Metadata:   a.version.Metadata(),
		},
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
			fmt.Sprintf("%s.%s.provisioner", a.name, a.cnf.ControllerConfig.InstanceUuid),
			"flock-provisioner",
			a.cnf.Log.Syslog.FormatRfc3164,
			a.cnf.Log.Syslog.StructuredData,
		)
	}
	return logging.Setup(opts)
}

// Runtime is the state of the app, for the command running it alone to count its resources in the metrics.
func (a *App) Runtime() *runtime.Runtime {
	return a.rt
}

// reportFailure asks the provisioner to stop. One failure is enough and nobody reads the channel once the stop began,
// so a failure finding it full is dropped instead of blocking its goroutine, which the stop waits for.
func reportFailure(hasError chan<- struct{}) {
	select {
	case hasError <- struct{}{}:
	default:
	}
}

// Run receives the resources and provisions them until ctx ends or a part fails.
func (a *App) Run(ctx context.Context) error {
	a.entry.Info().Str("name", a.name).Msg("Starting provisioner ...")
	wg := &sync.WaitGroup{}
	hasError := make(chan struct{}, 1)

	a.StartHandlers(wg, hasError)
	a.RunCertManager(wg)
	a.RunDocker(wg, hasError)
	a.RunServer(wg, hasError)
	a.RunChallengeServers(wg, hasError)

	select {
	case <-ctx.Done():
		a.entry.Info().Msg("Stop asked")
	case <-hasError:
		a.entry.Warn().Msg("one handler or the server failed")
	}
	return a.stop(wg)
}

// stop ends the app ctx first, so the handlers leave their stream before the aggregator closes the connection under
// them. The clients to the controller close only once everything stopped, because an obtain finishing writes back
// through them.
func (a *App) stop(wg *sync.WaitGroup) error {
	a.entry.Info().Msg("Gracefully stopping handlers and server ...")
	a.cancel()
	if err := a.aggregator.Close(); err != nil {
		a.entry.Error().Err(err).Msg("close aggregator failed")
	}
	stopped := make(chan struct{})
	go func() {
		wg.Wait()
		close(stopped)
	}()
	defer a.certman.Stop()
	defer a.grpcClients.Close()
	defer a.closeDockerClient()
	select {
	case <-stopped:
	case <-time.After(stopTimeout):
		return fmt.Errorf("timeout waiting for the handlers and the server to stop")
	}
	a.entry.Info().Msg("Provisioner stopped")
	return nil
}
