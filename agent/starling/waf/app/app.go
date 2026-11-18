package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/seagalia/flock/agent/starling/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"buf.build/go/protovalidate"
	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	grpcprom "github.com/grpc-ecosystem/go-grpc-middleware/providers/prometheus"
	grpclog "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	grpcvalidate "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/protovalidate"
	grpcrecovery "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/agent/starling/waf/wafproccessor"
	"github.com/seagalia/flock/sdk/logging"
	svcregister "github.com/seagalia/flock/sdk/starling_waf/services/register/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const (
	// envoy holds streams to the WAF as long as their requests last, a stop cuts the ones still open after this time
	defaultGracefulStopTimeout = 30 * time.Second
	metricsReadHeaderTimeout   = 10 * time.Second
)

// gracefulStopTimeout is a variable so a spec can check the fallback to Stop without waiting 30 seconds
var gracefulStopTimeout = defaultGracefulStopTimeout

// Embedded, the WAF shares the registry of the agent, whose sdk/rpcserver already registers the grpc_server_* names.
// Registered once for the process, as several apps can be created in the same process (tests).
var serverMetrics = grpcprom.NewServerMetrics(grpcprom.WithServerCounterOptions(grpcprom.WithNamespace("starling_waf")))

func init() {
	sdkmetrics.MustRegister(config.WafAppName, serverMetrics)
}

type App struct {
	entry             zerolog.Logger
	socketPath        string
	metricsSocketPath string
	grpcServer        *grpc.Server
}

// NewApp holds as many requests at once as memory allows. It serves no metrics when metricsSocketPath is empty.
// Embedded in the agent, the WAF needs none since its metrics are in the registry the agent already serves.
func NewApp(socketPath, metricsSocketPath string, memory uint64) (*App, error) {
	sdkmetrics.EnableAppMetrics(config.WafAppName)
	procServer, err := wafproccessor.NewEnvoyProcServer(memory)
	if err != nil {
		return nil, fmt.Errorf("create the WAF: %w", err)
	}

	opts, err := makeOptions()
	if err != nil {
		return nil, fmt.Errorf("create grpc options: %w", err)
	}

	grpcServer := grpc.NewServer(opts...)
	extProcPb.RegisterExternalProcessorServer(grpcServer, procServer)
	svcregister.RegisterRegisterServer(grpcServer, procServer)
	healthpb.RegisterHealthServer(grpcServer, health.NewServer())

	serverMetrics.InitializeMetrics(grpcServer)

	return &App{
		entry:             logging.Component("app"),
		socketPath:        socketPath,
		metricsSocketPath: metricsSocketPath,
		grpcServer:        grpcServer,
	}, nil
}

// Run serves until SIGINT or SIGTERM, then lets the running streams end.
func (a *App) Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// the first signal cancels ctx and gives the default behavior back, so a second one kills the process even when
	// the stop hangs
	context.AfterFunc(ctx, stop)

	return a.RunWithContext(ctx)
}

func (a *App) RunWithContext(ctx context.Context) error {
	lis, err := listenUnix(a.socketPath)
	if err != nil {
		return err
	}
	metricsServer, err := a.serveMetrics()
	if err != nil {
		_ = lis.Close()
		return err
	}

	// cancelled once Serve returns too, so a failed Serve still stops the metrics and removes the socket
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		a.stopGrpc()
		if metricsServer != nil {
			_ = metricsServer.Close()
		}
		err := os.Remove(a.socketPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			a.entry.Error().Err(err).Msg("failed to remove previous socket")
		}
	}()

	a.entry.Info().Str("socket", a.socketPath).Str("metrics_socket", a.metricsSocketPath).Msg("starting WAF server")
	// Serve returns as soon as the stop begins, the running streams end afterwards
	err = a.grpcServer.Serve(lis)
	cancel()
	<-stopped
	return err
}

func (a *App) stopGrpc() {
	graceful := make(chan struct{})
	go func() {
		defer close(graceful)
		a.grpcServer.GracefulStop()
	}()
	select {
	case <-graceful:
	case <-time.After(gracefulStopTimeout):
		a.entry.Warn().Msgf("WAF streams still open after %s, closing them", gracefulStopTimeout)
		// Stop also ends the GracefulStop still waiting
		a.grpcServer.Stop()
		<-graceful
	}
}

// serveMetrics gives no server when the app serves no metrics.
func (a *App) serveMetrics() (*http.Server, error) {
	if a.metricsSocketPath == "" {
		return nil, nil
	}
	lis, err := listenUnix(a.metricsSocketPath)
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	server := &http.Server{Handler: mux, ReadHeaderTimeout: metricsReadHeaderTimeout}
	go func() {
		err := server.Serve(lis)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.entry.Error().Err(err).Msg("metrics server stopped")
		}
	}()
	return server, nil
}

// listenUnix first removes the socket file a crashed run left behind, since net.Listen refuses an existing path.
func listenUnix(path string) (net.Listener, error) {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove previous socket %s: %w", path, err)
	}
	lis, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	return lis, nil
}

func makeOptions() ([]grpc.ServerOption, error) {
	validator, err := protovalidate.New()
	if err != nil {
		return nil, err
	}
	grpcOptions := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(wafproccessor.MaxMessageSize),
		grpc.ChainStreamInterceptor(
			grpcvalidate.StreamServerInterceptor(validator),
			grpcrecovery.StreamServerInterceptor(),
			grpclog.StreamServerInterceptor(interceptorLogger(logging.Component("grpc_server_stream"))),
			serverMetrics.StreamServerInterceptor(),
		),
		grpc.ChainUnaryInterceptor(
			grpcvalidate.UnaryServerInterceptor(validator),
			grpcrecovery.UnaryServerInterceptor(),
			grpclog.UnaryServerInterceptor(interceptorLogger(logging.Component("grpc_server_unary"))),
			serverMetrics.UnaryServerInterceptor(),
		),
	}
	return grpcOptions, nil
}

func interceptorLogger(l zerolog.Logger) grpclog.Logger {
	return grpclog.LoggerFunc(func(ctx context.Context, lvl grpclog.Level, msg string, fields ...any) {
		l := l.With().Fields(fields).Logger()

		switch lvl {
		case grpclog.LevelDebug:
			l.Debug().Msg(msg)
		case grpclog.LevelInfo:
			l.Info().Msg(msg)
		case grpclog.LevelWarn:
			l.Warn().Msg(msg)
		case grpclog.LevelError:
			l.Error().Msg(msg)
		default:
			panic(fmt.Sprintf("unknown level %v", lvl))
		}
	})
}
