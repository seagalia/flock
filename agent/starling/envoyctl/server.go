package envoyctl

import (
	clusterservice "github.com/envoyproxy/go-control-plane/envoy/service/cluster/v3"
	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	endpointservice "github.com/envoyproxy/go-control-plane/envoy/service/endpoint/v3"
	extensionconfigservice "github.com/envoyproxy/go-control-plane/envoy/service/extension/v3"
	listenerservice "github.com/envoyproxy/go-control-plane/envoy/service/listener/v3"
	routeservice "github.com/envoyproxy/go-control-plane/envoy/service/route/v3"
	runtimeservice "github.com/envoyproxy/go-control-plane/envoy/service/runtime/v3"
	secretservice "github.com/envoyproxy/go-control-plane/envoy/service/secret/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/delta/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/stream/v3"
	"github.com/rs/zerolog/log"
)

type ControlPlaneServer struct {
	delta delta.Server
}

func NewControlPlaneServer(deltaServer delta.Server) *ControlPlaneServer {
	return &ControlPlaneServer{
		delta: deltaServer,
	}
}

func (s *ControlPlaneServer) StreamAggregatedResources(discoverygrpc.AggregatedDiscoveryService_StreamAggregatedResourcesServer) error {
	log.Warn().Msg("StreamAggregatedResources called")
	return nil
}

func (s *ControlPlaneServer) DeltaStreamHandler(deltaStream stream.DeltaStream, typeURL string) error {
	return s.delta.DeltaStreamHandler(deltaStream, typeURL)
}

func (s *ControlPlaneServer) DeltaAggregatedResources(deltaStream discoverygrpc.AggregatedDiscoveryService_DeltaAggregatedResourcesServer) error {
	return s.DeltaStreamHandler(deltaStream, resource.AnyType)
}

func (s *ControlPlaneServer) DeltaEndpoints(deltaStream endpointservice.EndpointDiscoveryService_DeltaEndpointsServer) error {
	return s.DeltaStreamHandler(deltaStream, resource.EndpointType)
}

func (s *ControlPlaneServer) DeltaClusters(deltaStream clusterservice.ClusterDiscoveryService_DeltaClustersServer) error {
	return s.DeltaStreamHandler(deltaStream, resource.ClusterType)
}

func (s *ControlPlaneServer) DeltaRoutes(deltaStream routeservice.RouteDiscoveryService_DeltaRoutesServer) error {
	return s.DeltaStreamHandler(deltaStream, resource.RouteType)
}

func (s *ControlPlaneServer) DeltaScopedRoutes(deltaStream routeservice.ScopedRoutesDiscoveryService_DeltaScopedRoutesServer) error {
	return s.DeltaStreamHandler(deltaStream, resource.ScopedRouteType)
}

func (s *ControlPlaneServer) DeltaListeners(deltaStream listenerservice.ListenerDiscoveryService_DeltaListenersServer) error {
	return s.DeltaStreamHandler(deltaStream, resource.ListenerType)
}

func (s *ControlPlaneServer) DeltaSecrets(deltaStream secretservice.SecretDiscoveryService_DeltaSecretsServer) error {
	return s.DeltaStreamHandler(deltaStream, resource.SecretType)
}

func (s *ControlPlaneServer) DeltaRuntime(deltaStream runtimeservice.RuntimeDiscoveryService_DeltaRuntimeServer) error {
	return s.DeltaStreamHandler(deltaStream, resource.RuntimeType)
}

func (s *ControlPlaneServer) DeltaExtensionConfigs(deltaStream extensionconfigservice.ExtensionConfigDiscoveryService_DeltaExtensionConfigsServer) error {
	return s.DeltaStreamHandler(deltaStream, resource.ExtensionConfigType)
}

func (s *ControlPlaneServer) DeltaVirtualHosts(deltaStream routeservice.VirtualHostDiscoveryService_DeltaVirtualHostsServer) error {
	return s.DeltaStreamHandler(deltaStream, resource.VirtualHostType)
}
