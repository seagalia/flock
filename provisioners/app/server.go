package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/grpchealth/v2"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/seagalia/flock/provisioners/agentserv"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1/agentstatusconnect"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/rpcserver"
)

const (
	// defaultDrainGrace is how long the calls in flight have to end before their context is cancelled
	defaultDrainGrace = 10 * time.Second
	// shutdownTimeout is when a server still running is closed
	shutdownTimeout = 30 * time.Second
)

// drainGrace is defaultDrainGrace, a var only so specs don't wait the real grace
var drainGrace = defaultDrainGrace

// RunServer binds the server at once, so a port already taken fails the start, then serves the status, logs and metrics
// the controller reads until the app ctx ends. A failure is reported on hasError.
func (a *App) RunServer(wg *sync.WaitGroup, hasError chan struct{}) {
	servConf := a.cnf.ServerConfig
	agentServer := agentserv.NewServer(a.name, a.rt.PrevStore, a.rt.States, a.rt.SecretLinks, a.listenerHook, a.cnf.Acme.Enabled)
	connectServer := rpcserver.NewServer(simpleAuth)
	agentstatusconnect.RegisterResourceStatusHandler(connectServer, agentServer)
	agentstatusconnect.RegisterAgentStatusHandler(connectServer, agentServer)
	agentlogsconnect.RegisterLogsHandler(connectServer, agentServer)

	rootMux := http.NewServeMux()
	rpcserver.Mount(rootMux, connectServer, grpchealth.NewStaticChecker())
	rootMux.Handle("/metrics", promhttp.Handler())

	addr := fmt.Sprintf("%s:%d", servConf.ListenHost, servConf.ListenPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		a.entry.Error().Err(err).Str("server", addr).Msg("run server failed")
		reportFailure(hasError)
		return
	}
	wg.Go(func() {
		err := a.serveHttp(a.ctx, listener, rootMux, hasError)
		if err != nil {
			a.entry.Error().Err(err).Msg("Server did not stop gracefully")
			return
		}
		a.entry.Info().Msg("Server stopped")
	})
}

// serveHttp serves handler until ctx ends. The requests run on a drain ctx, which rpcserver.Shutdown cancels once
// drainGrace has passed. ctx ends first and would cut the calls in flight, and a log stream (Follow) never ends by
// itself, it would hold Shutdown until its timeout.
func (a *App) serveHttp(ctx context.Context, listener net.Listener, handler http.Handler, hasError chan struct{}) error {
	drainCtx, cancelDrain := context.WithCancel(context.Background())
	server := &http.Server{
		Handler:     handler,
		TLSConfig:   a.tlsConf,
		HTTP2:       rpcserver.HTTP2Config(),
		ErrorLog:    logging.HTTPServerErrorLog("http_server"),
		BaseContext: func(net.Listener) context.Context { return drainCtx },
	}
	entry := a.entry.With().Str("server", listener.Addr().String()).Logger()
	entry.Info().Msg("Starting connect and http server ...")
	serving := &sync.WaitGroup{}
	serving.Go(func() {
		err := server.ServeTLS(listener, "", "")
		if errors.Is(err, http.ErrServerClosed) {
			return
		}
		entry.Error().Err(err).Msg("Server error")
		reportFailure(hasError)
	})

	<-ctx.Done()
	err := rpcserver.Shutdown([]*http.Server{server}, cancelDrain, drainGrace, shutdownTimeout)
	serving.Wait()
	return err
}

// simpleAuth accepts only the clients with a certificate, which the tls config verified against the CA. The provisioner
// has no local tool calling it, so localhost gets no exception.
func simpleAuth(ctx context.Context, _ string) (context.Context, error) {
	client, ok := rpcserver.PeerFromContext(ctx)
	if !ok {
		return ctx, connect.NewError(connect.CodeUnauthenticated, "No peer")
	}
	if !client.HasClientCertificate() {
		return ctx, connect.NewError(connect.CodeUnauthenticated, "No peer certificate")
	}
	return ctx, nil
}
