package backendctl_test

import (
	"github.com/ArthurHlt/gohc"
	"github.com/seagalia/flock/agent/starling/testhelpers"
	"github.com/seagalia/flock/sdk/agentsdk/discoverers"
	agtesthelpers "github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/agent/starling/backendctl"
)

func TestResolvers(t *testing.T) {
	RegisterFailHandler(Fail)
	// first checks are spread over up to a second
	SetDefaultEventuallyTimeout(3 * time.Second)
	RunSpecs(t, "Resolvers Suite")
}

func setMockResolver(backendResolver *Resolver, mockHc *testhelpers.MockHealthChecker, mockDisco *agtesthelpers.MockDiscoverer) {
	if mockHc != nil {
		backendResolver.SetHealthCheckFactory(func(hc *core.HealthCheck, tlsServerName, rootCa string) (gohc.HealthChecker, error) {
			return mockHc, nil
		})
	}
	if mockDisco != nil {
		backendResolver.SetDiscovererFactory(func(backend *backend.Backend) (discoverers.Discoverer, error) {
			return mockDisco, nil
		})
	}

}
