package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"

	"connectrpc.com/grpchealth/v2"
	"github.com/moby/moby/client"
	"github.com/seagalia/vigie/sdk/vigie/eds/v1/edsconnect"

	"github.com/seagalia/flock/provisioners/docker/eds"
	"github.com/seagalia/flock/provisioners/docker/manager"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/rpcserver"
)

func (a *App) loadDockerManager() error {
	dockerConf := a.cnf.Docker
	if !dockerConf.Enabled {
		return nil
	}
	options := []client.Opt{client.FromEnv}
	if dockerConf.Host != "" {
		options = append(options, client.WithHost(dockerConf.Host))
	}
	dockerClient, err := client.New(options...)
	if err != nil {
		return fmt.Errorf("create docker client: %w", err)
	}
	a.dockerClient = dockerClient
	a.edsServer = eds.NewServer()
	controllerClient := a.grpcClients.Client(a.cnf.ControllerConfig.Addr)
	clients := manager.ControllerClients{
		Backends: svcbackendconnect.NewBackendClient(controllerClient),
		Certs:    svccertconnect.NewCertificateClient(controllerClient),
		Vss:      svcvsconnect.NewVirtualServerClient(controllerClient),
		Vips:     svcvipconnect.NewVIPClient(controllerClient),
	}
	a.dockerManager = manager.NewManager(a.cnf, dockerClient, a.edsServer, clients, a.provisionIdentity())
	return nil
}

// provisionIdentity gives the aggregator when an ads handler runs, the controller tells the uuid on its stream. With no
// handler no stream opens, so the docker manager writes the CN instead.
func (a *App) provisionIdentity() manager.AgentIdentity {
	hasAdsStream := len(a.rt.Handlers("")) > 0
	if hasAdsStream {
		return a.aggregator
	}
	return fixedIdentity(a.name)
}

type fixedIdentity string

func (f fixedIdentity) WaitAgentUuid(context.Context) (string, error) {
	return string(f), nil
}

// RunDocker serves the eds and keeps the controller in line with the containers until the app ctx ends. The eds
// failing to bind or the manager failing to start is reported on hasError.
func (a *App) RunDocker(wg *sync.WaitGroup, hasError chan struct{}) {
	if a.dockerManager == nil {
		return
	}
	connectServer := rpcserver.NewServer(acceptEveryCall)
	edsconnect.RegisterEndpointDiscoveryServiceHandler(connectServer, a.edsServer)
	mux := http.NewServeMux()
	rpcserver.Mount(mux, connectServer, grpchealth.NewStaticChecker())

	edsConf := a.cnf.Docker.Eds
	addr := net.JoinHostPort(edsConf.ListenHost, fmt.Sprint(edsConf.ListenPort))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		a.entry.Error().Err(err).Str("server", addr).Msg("run eds server failed")
		reportFailure(hasError)
		return
	}
	wg.Go(func() {
		err := a.serveHttp(a.ctx, listener, mux, hasError)
		if err != nil {
			a.entry.Error().Err(err).Msg("eds server did not stop gracefully")
		}
	})
	wg.Go(func() {
		err := a.dockerManager.Run(a.ctx)
		if err != nil {
			a.entry.Error().Err(err).Msg("docker manager failed")
			reportFailure(hasError)
		}
	})
}

// acceptEveryCall lets the agents in with no client certificate, which their eds client never sends. The eds only
// answers the ips and ports of containers.
func acceptEveryCall(ctx context.Context, _ string) (context.Context, error) {
	return ctx, nil
}

func (a *App) closeDockerClient() {
	if a.dockerClient == nil {
		return
	}
	err := a.dockerClient.Close()
	if err != nil {
		a.entry.Error().Err(err).Msg("close docker client failed")
	}
}
