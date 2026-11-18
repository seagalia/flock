package eds

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"

	"connectrpc.com/connect/v2"
	"github.com/moby/moby/api/types/container"
	"github.com/seagalia/flock/provisioners/docker/models"
	"github.com/seagalia/vigie/sdk/vigie/eds/v1"
	"github.com/seagalia/vigie/sdk/vigie/eds/v1/edsconnect"
)

type Server struct {
	mu       *sync.RWMutex
	services map[string][]*eds.Endpoint
	edsconnect.UnimplementedEndpointDiscoveryServiceHandler
}

func NewServer() *Server {
	return &Server{
		mu:       &sync.RWMutex{},
		services: map[string][]*eds.Endpoint{},
	}
}

func (s *Server) GetEndpoints(ctx context.Context, req *eds.GetEndpointsRequest) (*eds.GetEndpointsResponse, error) {
	s.mu.RLock()
	endpoints, ok := s.services[req.GetServiceName()]
	s.mu.RUnlock()
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, "service not found")
	}
	return &eds.GetEndpointsResponse{
		Endpoints: endpoints,
	}, nil
}

// Replace makes the listed containers the whole set the server answers, one endpoint per ready replica of each
// service.
func (s *Server) Replace(containers []container.Summary) error {
	services, err := groupByService(containers)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services = services
	return nil
}

func groupByService(containers []container.Summary) (map[string][]*eds.Endpoint, error) {
	services := map[string][]*eds.Endpoint{}
	var errs []error
	for _, summary := range containers {
		base, err := models.Decode(summary)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if base == nil || !base.Enable || !isReady(summary) {
			continue
		}
		endpoint, err := models.Endpoint(summary, base)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		services[base.Name] = append(services[base.Name], endpoint)
	}
	for _, endpoints := range services {
		slices.SortFunc(endpoints, func(a, b *eds.Endpoint) int {
			return cmp.Or(cmp.Compare(a.GetIp(), b.GetIp()), cmp.Compare(a.GetPort(), b.GetPort()))
		})
	}
	return services, errors.Join(errs...)
}

func isReady(summary container.Summary) bool {
	if summary.Health == nil {
		return true
	}
	status := summary.Health.Status
	return status != container.Starting && status != container.Unhealthy
}
