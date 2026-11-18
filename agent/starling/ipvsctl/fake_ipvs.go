package ipvsctl

import (
	"fmt"
	"github.com/cloudflare/ipvs"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/logging"
	"os"
	"strings"
	"sync"
)

// FakeIpvsClient is an in-memory ipvs client for tests and dev mode
type FakeIpvsClient struct {
	services     *sync.Map
	destinations *sync.Map
	entry        zerolog.Logger
}

func NewFakeIpvsClient() *FakeIpvsClient {
	return &FakeIpvsClient{
		services:     &sync.Map{},
		destinations: &sync.Map{},
		entry:        logging.Component("fake_ipvs_client"),
	}
}

func (f *FakeIpvsClient) Info() (ipvs.Info, error) {
	return ipvs.Info{
		Version:             [3]int{0, 0, 1},
		ConnectionTableSize: 100000,
	}, nil
}

func (f *FakeIpvsClient) Config() (ipvs.Config, error) {
	return ipvs.Config{}, nil
}

func (f *FakeIpvsClient) SetConfig(ipvs.Config) error {
	return nil
}

func (f *FakeIpvsClient) Services() ([]ipvs.ServiceExtended, error) {
	f.entry.Info().Msg("Services called")
	services := make([]ipvs.ServiceExtended, 0)
	f.services.Range(func(key, value any) bool {
		services = append(services, ipvs.ServiceExtended{
			Service: value.(ipvs.Service),
			Stats:   ipvs.Stats{},
			Stats64: ipvs.Stats{},
		})
		return true
	})
	return services, nil
}

func (f *FakeIpvsClient) Service(service ipvs.Service) (ipvs.ServiceExtended, error) {
	f.entry.Info().Msg("Service called")
	key := makeKeyService(service)
	value, ok := f.services.Load(key)
	if !ok {
		return ipvs.ServiceExtended{}, fmt.Errorf("service not found: %s", key)
	}
	return ipvs.ServiceExtended{
		Service: value.(ipvs.Service),
		Stats:   ipvs.Stats{},
		Stats64: ipvs.Stats{},
	}, nil
}

func (f *FakeIpvsClient) CreateService(service ipvs.Service) error {
	f.entry.Info().Str("service", makeKeyService(service)).Msg("CreateService called")
	key := makeKeyService(service)
	_, ok := f.services.Load(key)
	if ok {
		return fmt.Errorf("service already exists: %s", key)
	}
	f.services.Store(key, service)
	return nil
}

func (f *FakeIpvsClient) UpdateService(service ipvs.Service) error {
	f.entry.Info().Str("service", makeKeyService(service)).Msg("UpdateService called")
	key := makeKeyService(service)
	_, ok := f.services.Load(key)
	if !ok {
		return fmt.Errorf("service not found: %s", key)
	}
	f.services.Store(key, service)
	return nil
}

func (f *FakeIpvsClient) RemoveService(service ipvs.Service) error {
	f.entry.Info().Str("service", makeKeyService(service)).Msg("RemoveService called")
	key := makeKeyService(service)
	_, ok := f.services.Load(key)
	if !ok {
		return fmt.Errorf("service not found: %s", key)
	}
	f.services.Delete(key)
	// the kernel removes the destinations with their service
	f.destinations.Range(func(destKey, _ any) bool {
		if strings.HasPrefix(destKey.(string), key+"_") {
			f.destinations.Delete(destKey)
		}
		return true
	})
	return nil
}

func (f *FakeIpvsClient) Destinations(service ipvs.Service) ([]ipvs.DestinationExtended, error) {
	f.entry.Info().Str("service", makeKeyService(service)).Msg("Destinations called")
	destinations := make([]ipvs.DestinationExtended, 0)
	f.destinations.Range(func(key, value any) bool {
		if !strings.HasPrefix(key.(string), makeKeyService(service)) {
			return true
		}
		destinations = append(destinations, ipvs.DestinationExtended{
			Destination:           value.(ipvs.Destination),
			ActiveConnections:     1,
			InactiveConnections:   0,
			PersistentConnections: 1,
			Stats:                 ipvs.Stats{},
			Stats64:               ipvs.Stats{},
		})
		return true
	})
	// as the ipvs library does
	if len(destinations) == 0 {
		return nil, os.ErrNotExist
	}
	return destinations, nil
}

func (f *FakeIpvsClient) CreateDestination(service ipvs.Service, destination ipvs.Destination) error {
	f.entry.Info().
		Str("service", makeKeyService(service)).
		Str("destination", makeKeyDest(service, destination)).
		Str("fwd_method", fwdTypeToString(destination.FwdMethod)).
		Msg("CreateDestination called")
	key := makeKeyDest(service, destination)
	_, ok := f.destinations.Load(key)
	if ok {
		return fmt.Errorf("destination already exists: %s", key)
	}
	f.destinations.Store(key, destination)
	return nil
}

func (f *FakeIpvsClient) UpdateDestination(service ipvs.Service, destination ipvs.Destination) error {
	f.entry.Info().
		Str("service", makeKeyService(service)).
		Str("destination", makeKeyDest(service, destination)).
		Str("fwd_method", fwdTypeToString(destination.FwdMethod)).
		Msg("UpdateDestination called")
	key := makeKeyDest(service, destination)
	_, ok := f.destinations.Load(key)
	if !ok {
		return fmt.Errorf("destination not found: %s", key)
	}
	f.destinations.Store(key, destination)
	return nil
}

func (f *FakeIpvsClient) RemoveDestination(service ipvs.Service, destination ipvs.Destination) error {
	f.entry.Info().
		Str("service", makeKeyService(service)).
		Str("destination", makeKeyDest(service, destination)).
		Str("fwd_method", fwdTypeToString(destination.FwdMethod)).
		Msg("RemoveDestination called")
	key := makeKeyDest(service, destination)
	_, ok := f.destinations.Load(key)
	if !ok {
		return fmt.Errorf("destination not found: %s", key)
	}
	f.destinations.Delete(key)
	return nil
}
