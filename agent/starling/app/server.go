package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/grpchealth/v2"
	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/delta/v3"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"

	"github.com/seagalia/flock/agent/starling/agentserv"
	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/agent/starling/envoyctl"
	"github.com/seagalia/flock/sdk/agentsdk/proxmetrics"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	"github.com/seagalia/flock/sdk/flock/services/agent/manage/v1/agentmgmtconnect"
	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1/agentstatusconnect"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/rpcserver"
)

const (
	// defaultDrainGrace is how long the calls in flight have to end before their context is cancelled
	defaultDrainGrace = 10 * time.Second
	// shutdownTimeout is when a server still running is closed
	shutdownTimeout = 1 * time.Minute
)

// drainGrace is defaultDrainGrace, a var only so specs don't wait the real grace
var drainGrace = defaultDrainGrace

func (a *App) makeMetricsProxy() *proxmetrics.Fetcher {
	rawEnvoy := "http://localhost:19000/stats/prometheus?filter="
	envoyUrl, _ := url.Parse(rawEnvoy) // nolint
	a.cnf.MetricsConfig.ProxyMetricsConfig.Targets = append(
		a.cnf.MetricsConfig.ProxyMetricsConfig.Targets,
		&config.ProxyMetricsTarget{
			Name: "envoy",
			URL: config.URLConfig{
				URL: envoyUrl,
				Raw: rawEnvoy,
			},
		},
	)

	if !a.cnf.WafConfig.WafEmbedded {
		a.cnf.MetricsConfig.ProxyMetricsConfig.Targets = append(
			a.cnf.MetricsConfig.ProxyMetricsConfig.Targets,
			&config.ProxyMetricsTarget{
				Name:       "starling_waf",
				SocketPath: a.cnf.WafConfig.SocketMetricPath(),
			},
		)
	}

	tgts := make([]*proxmetrics.ProxyMetricsTarget, len(a.cnf.MetricsConfig.ProxyMetricsConfig.Targets))
	for i, t := range a.cnf.MetricsConfig.ProxyMetricsConfig.Targets {
		tgts[i] = &proxmetrics.ProxyMetricsTarget{
			Name:          t.Name,
			URL:           t.URL.URL,
			SocketPath:    t.SocketPath,
			User:          t.User,
			Password:      t.Password,
			Headers:       t.Headers,
			ScrapeTimeout: t.ScrapeTimeout,
		}
	}

	return proxmetrics.NewFetcher(
		proxmetrics.NewScraper(&tls.Config{
			InsecureSkipVerify: true,
		}),
		tgts,
	)
}

func (a *App) RunServer(wg *sync.WaitGroup, hasError chan struct{}) {
	err := a.runServer(wg, hasError)
	if err != nil {
		a.entry.Error().Err(err).Msg("run server failed")
		reportFailure(hasError)
	}
}

func (a *App) runServer(wg *sync.WaitGroup, hasError chan struct{}) error {
	servConf := a.cnf.ServerConfig
	if servConf == nil {
		return fmt.Errorf("agent: agent config is not defined")
	}

	// envoy's xDS is served by grpc-go, go-control-plane only works with it, with the same interceptors as connect
	envoyXdsServer := grpc.NewServer(grpc.StreamInterceptor(rpcserver.GrpcStreamInterceptor(simpleAuth)))
	controlPlaneServer := envoyctl.NewControlPlaneServer(
		delta.NewServer(a.ctx, a.configWatcher, envoyctl.NewServerCallbacks()),
	)
	discoverygrpc.RegisterAggregatedDiscoveryServiceServer(envoyXdsServer, controlPlaneServer)
	rpcserver.InitializeGrpcMetrics(envoyXdsServer)

	agentServer := agentserv.NewAgentServer(a.name, a.jointer, a.listenerHook, agentserv.TlsInfo{
		RootCaPool:   a.tlsConf.RootCAs,
		CAFilePath:   a.cnf.TlsConfig.CaFile,
		CertFilePath: a.cnf.TlsConfig.CertFile,
		KeyFilePath:  a.cnf.TlsConfig.KeyFile,
	}, a.isEnvoyAlive, a.isWafAlive, a.vipPlacer, a.rt.Store.VIPs(), a.rt.PrevStore, a.rt.States, a.rt.SecretLinks)
	connectServer := rpcserver.NewServer(simpleAuth)
	agentstatusconnect.RegisterResourceStatusHandler(connectServer, agentServer)
	agentstatusconnect.RegisterAgentStatusHandler(connectServer, agentServer)
	agentlogsconnect.RegisterLogsHandler(connectServer, agentServer)
	agentmgmtconnect.RegisterManageHandler(connectServer, agentServer)

	prometheus.MustRegister(agentServer)

	rootMux := http.NewServeMux()
	rpcserver.Mount(rootMux, connectServer, grpchealth.NewStaticChecker())
	rootMux.Handle("/"+discoverygrpc.AggregatedDiscoveryService_ServiceDesc.ServiceName+"/", envoyXdsServer)
	rootMux.Handle("/metrics", a.makeMetricsProxy())
	// the agent alone: parsing envoy's stats in the process would weigh on what is measured
	rootMux.Handle("/metrics/agent", proxmetrics.NewFetcher(nil, nil))
	a.entry.Info().Msg("Loading companion server.")
	companionServer := agentserv.NewCompanionServer(a.vipPlacer, a.netman, a.cnf.AdvertisementConfig.Interfaces)
	rootMux.HandleFunc("/companion/v1/ifaces", companionServer.ServeInterfacesInfo)
	a.entry.Info().Msg("Companion server loaded.")

	if a.vipPlacer != nil {
		a.entry.Info().Msg("Adding companion server events as vip placer loaded.")
		rootMux.HandleFunc("/companion/v1/events", companionServer.ServeEvents)
	}

	listeners, err := listenAll(serverAddrs(servConf.ListenHost, servConf.ListenPort))
	if err != nil {
		companionServer.Stop()
		return err
	}
	wg.Go(func() {
		defer companionServer.Stop()
		err := a.serveHttp(a.ctx, listeners, rootMux, hasError)
		if err != nil {
			a.entry.Error().Err(err).Msg("Servers did not stop gracefully")
			return
		}
		a.entry.Info().Msg("Servers stopped")
	})
	return nil
}

// listenAll binds every address before any is served, so a port already taken fails the start of the agent
func listenAll(addrs []string) ([]net.Listener, error) {
	listeners := make([]net.Listener, 0, len(addrs))
	for _, addr := range addrs {
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			for _, opened := range listeners {
				opened.Close() // nolint:errcheck
			}
			return nil, fmt.Errorf("listen on %s: %w", addr, err)
		}
		listeners = append(listeners, listener)
	}
	return listeners, nil
}

// serveHttp serves handler on every listener until ctx ends. The requests run on a drain ctx, which rpcserver.Shutdown
// cancels once drainGrace has passed. ctx ends first and would cut the calls in flight, and a log stream (Follow)
// never ends by itself, it would hold Shutdown until its timeout.
func (a *App) serveHttp(ctx context.Context, listeners []net.Listener, handler http.Handler, hasError chan struct{}) error {
	drainCtx, cancelDrain := context.WithCancel(context.Background())
	servers := make([]*http.Server, 0, len(listeners))
	serving := &sync.WaitGroup{}
	for _, listener := range listeners {
		server := &http.Server{
			Handler:     handler,
			TLSConfig:   a.tlsConf,
			HTTP2:       rpcserver.HTTP2Config(),
			ErrorLog:    logging.HTTPServerErrorLog("http_server"),
			BaseContext: func(net.Listener) context.Context { return drainCtx },
		}
		servers = append(servers, server)
		entry := a.entry.With().Str("server", listener.Addr().String()).Logger()
		entry.Info().Msg("Starting grpc and http server ...")
		serving.Go(func() {
			err := server.ServeTLS(listener, "", "")
			if errors.Is(err, http.ErrServerClosed) {
				entry.Info().Msg("Server not accepting connections anymore.")
				return
			}
			entry.Error().Err(err).Msg("Server error")
			reportFailure(hasError)
		})
	}

	<-ctx.Done()
	err := rpcserver.Shutdown(servers, cancelDrain, drainGrace, shutdownTimeout)
	serving.Wait()
	return err
}

// serverAddrs gives the exposed address and the loopback one instead of one listen on all interfaces,
// so the agent does not interfere with listeners made by lvs or envoy. An exposed address already on
// loopback (dev mode) gives one listener, two would bind the same port.
func serverAddrs(listenHost string, listenPort uint16) []string {
	exposedAddr := fmt.Sprintf("%s:%d", listenHost, listenPort)
	localAddr := fmt.Sprintf("127.0.0.1:%d", listenPort)
	if exposedAddr == localAddr {
		return []string{localAddr}
	}
	return []string{exposedAddr, localAddr}
}

// simpleAuth accepts calls from localhost (envoy, local tools) and from clients with a certificate (mtls)
func simpleAuth(ctx context.Context, _ string) (context.Context, error) {
	client, ok := rpcserver.PeerFromContext(ctx)
	if !ok {
		return ctx, connect.NewError(connect.CodeUnauthenticated, "No peer")
	}
	if strings.HasPrefix(client.Addr, "127.0.0.1:") {
		return ctx, nil
	}
	if !client.HasClientCertificate() {
		return ctx, connect.NewError(connect.CodeUnauthenticated, "No peer certificate")
	}
	return ctx, nil
}
