package models

import (
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/seagalia/vigie/sdk/vigie/eds/v1"
)

const defaultWeight = 1

// Endpoint is the address the agents reach the container on, in the network named by io.seagalia.flock.docker_network
// or its first one.
func Endpoint(summary container.Summary, base *Base) (*eds.Endpoint, error) {
	endpointSettings, err := pickNetwork(summary, base)
	if err != nil {
		return nil, err
	}
	ip, err := networkIp(endpointSettings)
	if err != nil {
		return nil, fmt.Errorf("container %s: %w", summary.ID, err)
	}
	port, err := containerPort(summary, base)
	if err != nil {
		return nil, fmt.Errorf("container %s: %w", summary.ID, err)
	}
	weight := uint32(defaultWeight)
	if base.Backend != nil && base.Backend.Weight != nil {
		weight = uint32(*base.Backend.Weight)
	}
	return &eds.Endpoint{Ip: ip.String(), Port: uint32(port), LoadBalancingWeight: weight}, nil
}

func pickNetwork(summary container.Summary, base *Base) (*network.EndpointSettings, error) {
	var networks map[string]*network.EndpointSettings
	if summary.NetworkSettings != nil {
		networks = summary.NetworkSettings.Networks
	}
	names := slices.Sorted(maps.Keys(networks))
	if len(names) == 0 {
		return nil, fmt.Errorf("container %s has no network to reach it on", summary.ID)
	}

	if base.DockerNetwork != nil {
		wanted := *base.DockerNetwork
		// compose prefixes the networks it creates with the project: lb in the compose file is <project>_lb
		composeName := summary.Labels[composeProjectLabel] + "_" + wanted
		for _, name := range []string{wanted, composeName} {
			if settings, found := networks[name]; found {
				return settings, nil
			}
		}
		return nil, fmt.Errorf("container %s is not on network %s, only on %v", summary.ID, wanted, names)
	}

	// compose sets the network mode to the first network of the service
	settings, found := networks[summary.HostConfig.NetworkMode]
	if found {
		return settings, nil
	}
	return networks[names[0]], nil
}

func networkIp(settings *network.EndpointSettings) (netip.Addr, error) {
	if settings.IPAddress.IsValid() {
		return settings.IPAddress, nil
	}
	if settings.GlobalIPv6Address.IsValid() {
		return settings.GlobalIPv6Address, nil
	}
	return netip.Addr{}, errors.New("no ip on its network")
}

// containerPort takes io.seagalia.flock.backend.port, else the single port the container exposes for the protocol.
// Docker lists a published port once per address family, so the ports are deduplicated.
func containerPort(summary container.Summary, base *Base) (Port, error) {
	if base.Backend != nil && base.Backend.Port != nil {
		return *base.Backend.Port, nil
	}
	portType := "tcp"
	if base.protocol() == ProtocolUDP {
		portType = "udp"
	}
	var exposed []uint16
	for _, port := range summary.Ports {
		isNew := port.Type == portType && !slices.Contains(exposed, port.PrivatePort)
		if isNew {
			exposed = append(exposed, port.PrivatePort)
		}
	}
	if len(exposed) != 1 {
		return 0, fmt.Errorf("set %sbackend.port, the container exposes the %s ports %v", LabelPrefix, portType, exposed)
	}
	return Port(exposed[0]), nil
}
