package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/grpchealth/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/seagalia/flock/agent/f5/agentserv"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	"github.com/seagalia/flock/sdk/flock/services/agent/manage/v1/agentmgmtconnect"
	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1/agentstatusconnect"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/rpcserver"
)

const (
	statusCacheTTL = 10 * time.Second
	// defaultDrainGrace is how long the calls in flight have to end before their context is cancelled
	defaultDrainGrace = 10 * time.Second
	// shutdownTimeout is when a server still running is closed
	shutdownTimeout = 1 * time.Minute
)

// drainGrace is defaultDrainGrace, a var only so specs don't wait the real grace
var drainGrace = defaultDrainGrace

// RunServer binds the servers at once, so a port already taken fails the start, then serves them until the app ctx
// ends. A failure is reported on hasError.
func (a *App) RunServer(wg *sync.WaitGroup, hasError chan struct{}) {
	servConf := a.cnf.ServerConfig
	statusDevice := agentserv.NewCachedDevice(a.device, statusCacheTTL)
	agentServer := agentserv.NewAgentServer(a.name, a.rec, statusDevice, a.listenerHook, a.rt.Store.Backends(), a.rt.PrevStore, a.rt.States, a.rt.SecretLinks, agentserv.TlsInfo{
		RootCaPool:   a.tlsConf.RootCAs,
		CAFilePath:   a.cnf.TlsConfig.CaFile,
		CertFilePath: a.cnf.TlsConfig.CertFile,
		KeyFilePath:  a.cnf.TlsConfig.KeyFile,
	})
	connectServer := rpcserver.NewServer(simpleAuth)
	agentstatusconnect.RegisterResourceStatusHandler(connectServer, agentServer)
	agentstatusconnect.RegisterAgentStatusHandler(connectServer, agentServer)
	agentlogsconnect.RegisterLogsHandler(connectServer, agentServer)
	agentmgmtconnect.RegisterManageHandler(connectServer, agentServer)

	rootMux := http.NewServeMux()
	rpcserver.Mount(rootMux, connectServer, grpchealth.NewStaticChecker())
	rootMux.Handle("/metrics", promhttp.Handler())

	listeners, err := listenAll(serverAddrs(servConf.ListenHost, servConf.ListenPort))
	if err != nil {
		a.entry.Error().Err(err).Msg("run server failed")
		reportFailure(hasError)
		return
	}
	wg.Go(func() {
		err := a.serveHttp(a.ctx, listeners, rootMux, hasError)
		if err != nil {
			a.entry.Error().Err(err).Msg("Servers did not stop gracefully")
			return
		}
		a.entry.Info().Msg("Servers stopped")
	})
}

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
		entry.Info().Msg("Starting connect and http server ...")
		serving.Go(func() {
			err := server.ServeTLS(listener, "", "")
			if errors.Is(err, http.ErrServerClosed) {
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

func serverAddrs(listenHost string, listenPort uint16) []string {
	exposedAddr := fmt.Sprintf("%s:%d", listenHost, listenPort)
	localAddr := fmt.Sprintf("127.0.0.1:%d", listenPort)
	if exposedAddr == localAddr {
		return []string{localAddr}
	}
	return []string{exposedAddr, localAddr}
}

// simpleAuth accepts calls from localhost (local tools) and from clients with a certificate (the controller, mtls).
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
