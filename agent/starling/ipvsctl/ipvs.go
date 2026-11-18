package ipvsctl

import (
	"fmt"
	"net"
	"slices"
	"sync"

	"github.com/hashicorp/go-multierror"

	"github.com/cloudflare/ipvs"
	"github.com/seagalia/flock/sdk/logging"
)

var entry = logging.Component("ipvsctl")

// route handlers of tcp and udp check schedulers concurrently
var (
	canUseIPVSProxier      = make(map[string]error)
	canUseIPVSProxierMutex sync.Mutex
)

var client ipvs.Client

func SetClient(c ipvs.Client) {
	client = c
}

func Info() (ipvs.Info, error) {
	return client.Info()
}

func Services() ([]ipvs.ServiceExtended, error) {
	return client.Services()
}

func Service(svc ipvs.Service) (ipvs.ServiceExtended, error) {
	return client.Service(svc)
}

func CreateService(svc ipvs.Service) error {
	entSvc := entry.With().Str("service", makeKeyService(svc)).Logger()
	entSvc.Debug().Msg("Creating service ...")
	err := client.CreateService(svc)
	if err != nil {
		return err
	}
	entSvc.Debug().Msg("Service created.")
	return nil
}

func UpdateService(svc ipvs.Service) error {
	entSvc := entry.With().Str("service", makeKeyService(svc)).Logger()
	entSvc.Debug().Msg("Updating service ...")
	err := client.UpdateService(svc)
	if err != nil {
		return err
	}
	entSvc.Debug().Msg("Service updated.")
	return nil
}

func RemoveService(svc ipvs.Service) error {
	entSvc := entry.With().Str("service", makeKeyService(svc)).Logger()
	entSvc.Debug().Msg("Removing service ...")
	err := client.RemoveService(svc)
	if err != nil {
		return err
	}
	entSvc.Debug().Msg("Service removed.")
	return nil
}

func Destinations(svc ipvs.Service) ([]ipvs.DestinationExtended, error) {
	return client.Destinations(svc)
}

func CreateDestination(svc ipvs.Service, dst ipvs.Destination) error {
	entSvc := entry.With().
		Str("service", makeKeyService(svc)).
		Str("fwd_method", fwdTypeToString(dst.FwdMethod)).
		Str("destination", makeKeyDest(svc, dst)).
		Logger()
	entSvc.Debug().Msg("Creating destination ...")
	err := client.CreateDestination(svc, dst)
	if err != nil {
		return err
	}
	entSvc.Debug().Msg("Destination created.")
	return nil
}

func UpdateDestination(svc ipvs.Service, dst ipvs.Destination) error {
	entSvc := entry.With().
		Str("service", makeKeyService(svc)).
		Str("fwd_method", fwdTypeToString(dst.FwdMethod)).
		Str("destination", makeKeyDest(svc, dst)).
		Logger()
	entSvc.Debug().Msg("Updating destination ...")
	err := client.UpdateDestination(svc, dst)
	if err != nil {
		return err
	}
	entSvc.Debug().Msg("Destination updated.")
	return nil
}

func RemoveDestination(svc ipvs.Service, dst ipvs.Destination) error {
	entSvc := entry.With().
		Str("service", makeKeyService(svc)).
		Str("fwd_method", fwdTypeToString(dst.FwdMethod)).
		Str("destination", makeKeyDest(svc, dst)).
		Logger()
	entSvc.Debug().Msg("Removing destination ...")
	err := client.RemoveDestination(svc, dst)
	if err != nil {
		return err
	}
	entSvc.Debug().Msg("Destination removed.")
	return nil
}

// RemoveServicesOn removes the services listening on one of the ips, whatever their port and protocol. Services on
// other addresses are left alone, they may belong to another user of ipvs on the host.
func RemoveServicesOn(ips []net.IP) error {
	if len(ips) == 0 {
		return nil
	}
	services, err := client.Services()
	if err != nil {
		return fmt.Errorf("list services: %w", err)
	}
	var result error
	for _, service := range services {
		serviceIp := ServiceToIpNet(service.Service).IP
		isOnIps := slices.ContainsFunc(ips, serviceIp.Equal)
		if !isOnIps {
			continue
		}
		err := RemoveService(service.Service)
		if err != nil {
			result = multierror.Append(result, fmt.Errorf("remove service %s: %w", makeKeyService(service.Service), err))
		}
	}
	return result
}

func ServiceToIpNet(svc ipvs.Service) *net.IPNet {
	return &net.IPNet{
		IP:   net.IP(svc.Address.Unmap().AsSlice()),
		Mask: net.IPMask(svc.Netmask.AsSlice()),
	}
}

// CanUseIPVSProxier tells if ipvs can balance with the scheduler: an existing virtual server (VS) using it
// is enough, otherwise a dummy VS is created then removed, which loads the kernel module of the scheduler.
// The result is cached per scheduler. Adapted from
// https://github.com/kubernetes/kubernetes/blob/f78e722310e50bcaca9276be22276d9e91d91308/pkg/proxy/ipvs/supported.go
func CanUseIPVSProxier(scheduler string) error {
	if scheduler == "" {
		scheduler = "rr"
	}
	canUseIPVSProxierMutex.Lock()
	defer canUseIPVSProxierMutex.Unlock()
	errorCanUse, isCached := canUseIPVSProxier[scheduler]
	if isCached {
		return errorCanUse
	}
	logger := entry.With().Logger()
	if client == nil {
		logger.Error().Msg("ipvs not supported by the kernel")
		err := fmt.Errorf("ipvs not supported by the kernel")
		canUseIPVSProxier[scheduler] = err
		return err
	}
	_, isFakeClient := client.(*FakeIpvsClient)
	if isFakeClient {
		canUseIPVSProxier[scheduler] = nil
		return nil
	}

	// an unreadable ipvs table makes the probe below fail
	vservers, _ := Services() // nolint: errcheck
	for _, vs := range vservers {
		if vs.Scheduler == scheduler {
			logger.Debug().Str("scheduler", scheduler).Msg("Existing VS uses the configured scheduler")
			canUseIPVSProxier[scheduler] = nil
			return nil
		}
	}

	// 198.51.100.0 is in TEST-NET-2 (RFC 5737), reserved for documentation: no real VS should use it.
	// A VS on this address would be broken by the probe.
	vs := ipvs.Service{
		Address:   AddrFromIP(net.ParseIP("198.51.100.0")),
		Netmask:   MaskFromIPMask(net.CIDRMask(32, 32)),
		Scheduler: scheduler,
		Port:      20000,
		Family:    ipvs.INET,
		Protocol:  ipvs.TCP,
	}
	if err := CreateService(vs); err != nil {
		logger.Error().Err(err).Str("scheduler", scheduler).Msg("Could not create dummy VS")
		canUseIPVSProxier[scheduler] = err
		return err
	}

	// some kernels accept the creation without adding the VS
	vservers, err := Services()
	if err != nil {
		logger.Error().Err(err).Str("scheduler", scheduler).Msg("Could not read the ipvs")
		canUseIPVSProxier[scheduler] = err
		return err
	}
	logger.Debug().Int("count", len(vservers)).Msg("Virtual Servers after adding dummy")
	if len(vservers) == 0 {
		logger.Debug().Str("scheduler", scheduler).Msg("Dummy VS not created")
		err := fmt.Errorf("ipvs not supported")
		canUseIPVSProxier[scheduler] = err
		return err
	}
	logger.Debug().Str("scheduler", scheduler).Msg("Dummy VS created")

	if err := RemoveService(vs); err != nil {
		logger.Error().Err(err).Str("scheduler", scheduler).Msg("Could not delete dummy VS")
		canUseIPVSProxier[scheduler] = err
		return err
	}
	canUseIPVSProxier[scheduler] = nil
	return nil
}
