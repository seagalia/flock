package app

import (
	"fmt"
	"net"

	"github.com/hashicorp/go-multierror"
	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/agent/starling/ipvsctl"
	"github.com/seagalia/flock/agent/starling/nm"
	"github.com/seagalia/flock/sdk/logging"
)

// CleanHost removes what a stopped agent left on its host from the host alone, the controller is never contacted:
// the ipvs services of the vips of the dummy interface and the copies of the wildcard vips, then the nftables table
// and the dummy interface. The announces stop with the agent process. An envoy managed by the service manager of the
// host keeps its last configuration until it restarts, but its listeners lose their vips with the dummy interface.
func CleanHost(cnf *config.Config) error {
	entry := logging.Component("clean")
	if cnf.DevMode {
		entry.Info().Msg("dev mode never changes the host, nothing to clean")
		return nil
	}
	nmConf := *cnf.AdvertisementConfig.NetworkManager

	// read before the dummy interface goes away with them
	vips, err := nm.Vips(nmConf)
	if err != nil {
		return fmt.Errorf("list the vips: %w", err)
	}
	var result error
	err = removeIpvsServices(vips)
	if err != nil {
		result = multierror.Append(result, err)
	}
	err = nm.Cleanup(nmConf)
	if err != nil {
		result = multierror.Append(result, err)
	}
	entry.Info().Int("vips", len(vips)).Msg("host cleaned")
	return result
}

// the copies of the wildcard vips sit on the addresses of the machine, out of the dummy interface, so only the state
// file finds them
func removeIpvsServices(vips []net.IP) error {
	err := ipvsctl.Init()
	if err != nil {
		return fmt.Errorf("open ipvs: %w", err)
	}
	var result error
	err = ipvsctl.RemoveServicesOn(vips)
	if err != nil {
		result = multierror.Append(result, fmt.Errorf("remove the ipvs services: %w", err))
	}
	err = ipvsctl.RemoveRecordedCopies(ipvsctl.CopiesStateFile)
	if err != nil {
		result = multierror.Append(result, fmt.Errorf("remove the copies of the wildcard ipvs services: %w", err))
	}
	return result
}
