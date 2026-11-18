package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/seagalia/flock/controller/announcers"
	"github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/controller/permissioners"
	"github.com/seagalia/flock/controller/serves"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	"github.com/seagalia/flock/sdk/flock/services/core/v1/svccoreconnect"
	"github.com/seagalia/flock/sdk/flock/services/discovery/v1/svcdiscoveryconnect"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	"github.com/seagalia/flock/sdk/flock/services/lease/v1/svcleaseconnect"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	"github.com/seagalia/flock/sdk/flock/services/permission/v1/svcpermconnect"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/rpcserver"
	"github.com/seagalia/flock/webui"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
)

const (
	// drainGrace is how long the calls in flight have to end before their context is cancelled
	drainGrace = 10 * time.Second
	// shutdownTimeout is when a server still running is closed
	shutdownTimeout = 1 * time.Minute
	// handlersTimeout bounds the wait for the handlers still running once the servers have stopped
	handlersTimeout = 30 * time.Second
)

func (a *App) loadPermissioner() {
	a.permissioner = permissioners.NewMetrics(permissioners.NewDbStorePerm(a.storeManager))
}

func (a *App) makeTlsServerConfig() (*tls.Config, error) {
	tlsConf := a.cnf.Server.Tls
	cert, err := tls.LoadX509KeyPair(tlsConf.CertFile, tlsConf.KeyFile)
	if err != nil {
		return nil, err
	}
	tlsCredsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	var caCertPool *x509.CertPool
	if tlsConf.CaFile != "" {
		caCertPool = x509.NewCertPool()
		caCert, err := os.ReadFile(tlsConf.CaFile)
		if err != nil {
			return nil, fmt.Errorf("agent: failed to load ca certificate: %v", err)
		}

		caCertPool.AppendCertsFromPEM(caCert)
		tlsCredsConf.ClientAuth = tls.VerifyClientCertIfGiven
		a.entry.Debug().Msg("Mtls authentication enabled.")
	}
	tlsCredsConf.ClientCAs = caCertPool
	return tlsCredsConf, nil
}

func (a *App) RunServer(ctx context.Context) error {

	servConf := a.cnf.Server
	if servConf == nil {
		return a.abort(fmt.Errorf("controller: server config is not defined"))
	}
	jwtMan, err := svclogin.NewJwtManager(servConf.JwtMaker)
	if err != nil {
		return a.abort(fmt.Errorf("controller: %w", err))
	}
	login, err := auth.NewLogin(servConf.Login)
	if err != nil {
		return a.abort(fmt.Errorf("controller: %w", err))
	}

	a.entry.Debug().Str("type", a.permissioner.Name()).Msg("Permissioner loaded.")

	linker := announcers.NewLink(ctx, a.storeManager)

	admConf := a.cnf.Server.AdminConfig

	server := serves.NewFlockServer(
		a.ctx,
		a.mtlsConf,
		admConf,
		linker,
		a.storeManager,
		jwtMan,
		login,
		a.permissioner,
		a.instantiater,
		a.ipam,
		a.locker,
		a.leases,
	)
	tlsCredsConf, err := a.makeTlsServerConfig()
	if err != nil {
		return a.abort(err)
	}
	// the server authenticates every call with AuthFuncOverride: agents by mtls, users by token
	connectServer := rpcserver.NewServer(server.AuthFuncOverride)
	svcdiscoveryconnect.RegisterAggregatedDiscoveryHandler(connectServer, server)
	svcloginconnect.RegisterLoginHandler(connectServer, server)
	svccertconnect.RegisterCertificateHandler(connectServer, server)
	svcsecretconnect.RegisterSecretHandler(connectServer, server)
	svcleaseconnect.RegisterLeaseHandler(connectServer, server)
	svcvsconnect.RegisterVirtualServerHandler(connectServer, server)
	svcbackendconnect.RegisterBackendHandler(connectServer, server)
	svcinstanceconnect.RegisterInstanceHandler(connectServer, server)
	svcvipconnect.RegisterVIPHandler(connectServer, server)
	svclookupconnect.RegisterLookupHandler(connectServer, server)
	svccoreconnect.RegisterInfoHandler(connectServer, server)
	svcpermconnect.RegisterPermissionHandler(connectServer, server)
	svczoneconnect.RegisterZoneHandler(connectServer, server)

	healthDependencies := []dependency{{name: "database", pinger: a.dbMan}}
	if a.nats != nil {
		healthDependencies = append(healthDependencies, dependency{name: "nats", pinger: a.nats})
	}

	rootMux := http.NewServeMux()
	rpcserver.Mount(rootMux, connectServer, newHealthChecker(healthDependencies...))
	rootMux.Handle("/metrics", promhttp.Handler())
	rootMux.Handle("/metrics/{instance_uuid}", a.metricsFetcher)

	if a.testRoutes != nil {
		a.testRoutes(rootMux)
	}

	if servConf.Dashboard.Enabled {
		webui.Mount(rootMux, "/dashboard", webui.Browser())
		a.entry.Info().Msgf("Dashboard enabled on %s .", makeHttpsUrl(servConf.ListenAddr, "/dashboard/"))
	}

	// requests run on drainCtx, which rpcserver.Shutdown cancels once the grace has passed. a.ctx is cancelled first
	// and would cut the unary calls still running.
	drainCtx, cancelDrain := context.WithCancel(context.Background())
	baseContext := func(net.Listener) context.Context { return drainCtx }
	handlers := &inFlight{}
	handler := handlers.track(rootMux)
	defaultServer := &http.Server{
		Addr:        servConf.ListenAddr,
		Handler:     handler,
		TLSConfig:   tlsCredsConf,
		HTTP2:       rpcserver.HTTP2Config(),
		ErrorLog:    logging.HTTPServerErrorLog("http_server"),
		BaseContext: baseContext,
	}
	// buffered for both servers, a listener failing after the stop must not block its goroutine
	listenErr := make(chan error, 2)
	servers := []*http.Server{defaultServer}
	a.entry.Info().Msgf("Starting grpc and http server on %s ...", servConf.ListenAddr)
	go func() {
		listenErr <- listenEnd(defaultServer.ListenAndServeTLS(a.cnf.Server.Tls.CertFile, a.cnf.Server.Tls.KeyFile))
	}()
	if servConf.LoopbackHttpAddr != "" {
		loopbackServer := &http.Server{
			Addr:        servConf.LoopbackHttpAddr,
			Handler:     handler,
			ErrorLog:    logging.HTTPServerErrorLog("http_server"),
			BaseContext: baseContext,
		}
		servers = append(servers, loopbackServer)
		a.entry.Info().Msgf("Serving the same routes over plain http on %s, for a browser on this machine.", servConf.LoopbackHttpAddr)
		go func() {
			listenErr <- listenEnd(loopbackServer.ListenAndServe())
		}()
	}

	// multi publishes the changes of the calls still in flight to the other controllers, it stops after the servers
	multiCtx, cancelMulti := context.WithCancel(context.Background())
	multiStopped := make(chan struct{})
	if a.multiRunnable != nil {
		go func() {
			defer close(multiStopped)
			log.Info().Msg("Starting multi runnable ...")
			err := a.multiRunnable.Run(multiCtx)
			if err != nil {
				log.Error().Msgf("Failed to run multi runnable: %v", err)
			}
			log.Info().Msg("Multi runnable stopped.")
		}()
	} else {
		close(multiStopped)
	}

	var serveErr error
	select {
	case <-ctx.Done():
		a.entry.Info().Msg("Stop asked, gracefully stopping servers ...")
	case serveErr = <-listenErr:
		a.entry.Error().Err(serveErr).Msg("A server stopped, stopping the others ...")
	}
	return errors.Join(serveErr, a.stop(servers, handlers, server, cancelDrain, cancelMulti, multiStopped))
}

// abort releases what NewApp opened when RunServer fails before serving
func (a *App) abort(err error) error {
	a.cancelFunc()
	return errors.Join(err, a.dbMan.Close())
}

// stop ends the parts of the app in dependency order. The servers go first, then multi which publishes the changes of
// their last calls, then the database they all use.
func (a *App) stop(servers []*http.Server, handlers *inFlight, calls *serves.FlockServer,
	cancelDrain, cancelMulti context.CancelFunc, multiStopped <-chan struct{}) error {
	a.cancelFunc()
	shutdownErr := rpcserver.Shutdown(servers, cancelDrain, drainGrace, shutdownTimeout)
	if shutdownErr != nil {
		a.entry.Error().Err(shutdownErr).Msg("Servers did not stop gracefully")
	} else {
		a.entry.Info().Msg("All servers stopped gracefully")
	}
	// HTTP/2 counts a stream closed as soon as its client resets it, so Shutdown can return while its handler still
	// runs the defers that touch the store
	// the work the calls left running writes in the store and publishes its changes through multi. A handler still
	// running could start more of it, which a wait can't take.
	if handlers.wait(handlersTimeout) {
		calls.WaitBackground()
	} else {
		a.entry.Warn().Msgf("Handlers still running after %s, closing the database under them", handlersTimeout)
	}
	cancelMulti()
	<-multiStopped
	return errors.Join(shutdownErr, a.dbMan.Close())
}

// inFlight counts the requests whose handler has not returned yet. A sync.WaitGroup would forbid a request arriving
// at a zero count while the stop waits, which a stream opened just before the shutdown can do.
type inFlight struct {
	mutex sync.Mutex
	count int
	// idle is closed by the handler returning last once a wait started
	idle chan struct{}
}

func (f *inFlight) track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.add()
		defer f.done()
		next.ServeHTTP(w, r)
	})
}

func (f *inFlight) add() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.count++
}

func (f *inFlight) done() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.count--
	if f.count == 0 && f.idle != nil {
		close(f.idle)
		f.idle = nil
	}
}

// wait returns false when the timeout passed before every handler returned
func (f *inFlight) wait(timeout time.Duration) bool {
	f.mutex.Lock()
	if f.count == 0 {
		f.mutex.Unlock()
		return true
	}
	idle := make(chan struct{})
	f.idle = idle
	f.mutex.Unlock()
	select {
	case <-idle:
		return true
	case <-time.After(timeout):
		return false
	}
}

// listenEnd gives the error of a server that stopped serving, nil when it was asked to stop
func listenEnd(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func makeHttpsUrl(listen, path string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "https://" + listen + path
	}
	if host != "0.0.0.0" {
		return "https://" + listen + path
	}
	return fmt.Sprintf("https://%s:%s%s", GetOutboundIP(), port, path)
}

func GetOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal().Msg(err.Error())
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}
