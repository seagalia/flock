package envoyctl

import (
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func makeEnvoyClusterLoadAssignment(backendUuid string, ipAddresses []*core.IPAddr) *endpointv3.ClusterLoadAssignment {
	return &endpointv3.ClusterLoadAssignment{
		ClusterName: backendUuid,
		Endpoints: []*endpointv3.LocalityLbEndpoints{
			{
				LbEndpoints: ipAddressesToLbEndpoints(ipAddresses),
			},
		},
	}
}

func ipAddressesToLbEndpoints(ipAddresses []*core.IPAddr) []*endpointv3.LbEndpoint {
	lbEndpoints := make([]*endpointv3.LbEndpoint, len(ipAddresses))
	for i, ipAddr := range ipAddresses {
		lbEndpoints[i] = ipAddrToLbEndpoint(ipAddr)
	}
	return lbEndpoints
}

func ipAddrToLbEndpoint(ipAddr *core.IPAddr) *endpointv3.LbEndpoint {
	endpt := &endpointv3.LbEndpoint{
		HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
			Endpoint: &endpointv3.Endpoint{
				Address: &corev3.Address{
					Address: &corev3.Address_SocketAddress{
						SocketAddress: &corev3.SocketAddress{
							Address: ipAddr.GetIp(),
							PortSpecifier: &corev3.SocketAddress_PortValue{
								PortValue: ipAddr.GetPort(),
							},
						},
					},
				},
			},
		},
	}

	if ipAddr.GetLoadBalancingWeight() > 0 {
		endpt.LoadBalancingWeight = &wrapperspb.UInt32Value{
			Value: ipAddr.GetLoadBalancingWeight(),
		}
	}
	return endpt
}
