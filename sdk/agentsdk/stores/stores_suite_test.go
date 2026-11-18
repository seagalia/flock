package stores_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/logging"

	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
)

func TestStores(t *testing.T) {
	RegisterFailHandler(Fail)
	if err := logging.Setup(logging.Options{Console: GinkgoWriter}); err != nil {
		t.Fatal(err)
	}
	RunSpecs(t, "Stores Suite")
}

// httpVs has one virtual host per domain, routing to its backend
func httpVs(uuid string, domainToBackend map[string]string) *vs.VirtualServer {
	vhosts := make([]*route.VirtualHost, 0, len(domainToBackend))
	for domain, backendUuid := range domainToBackend {
		vhosts = append(vhosts, &route.VirtualHost{
			Domains: []string{domain},
			Routes:  []*route.Route{{Action: &route.Route_BackendUuid{BackendUuid: backendUuid}}},
		})
	}
	return &vs.VirtualServer{
		UUID:        uuid,
		Name:        uuid,
		Port:        80,
		RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{VirtualHosts: vhosts}},
	}
}

func tcpVs(uuid string, port uint32, backendUuid string) *vs.VirtualServer {
	return &vs.VirtualServer{
		UUID:        uuid,
		Port:        port,
		RouteConfig: &vs.VirtualServer_TcpRouteConfig{TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: backendUuid}},
	}
}

func udpVs(uuid string, port uint32, backendUuid string) *vs.VirtualServer {
	return &vs.VirtualServer{
		UUID:        uuid,
		Port:        port,
		RouteConfig: &vs.VirtualServer_UdpRouteConfig{UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: backendUuid}},
	}
}
