package backendctl

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

var _ = Describe("Joint stop", func() {
	It("should return only once every resolver finished", func() {
		joint := NewJoint(context.Background(), stores.NewStateStore())
		for _, uuid := range []string{"backend-1", "backend-2"} {
			Expect(joint.OnMessageAdded(context.Background(), &backend.Backend{
				UUID:        uuid,
				Name:        uuid,
				RefreshRate: durationpb.New(time.Minute),
				Endpoint: &backend.Backend_IpAddrGroup{
					IpAddrGroup: &core.IPAddrGroup{IpAddresses: []*core.IPAddr{{Ip: "127.0.0.1", Port: 8080}}},
				},
				HealthCheck: &core.HealthCheck{
					Timeout:       durationpb.New(time.Second),
					Interval:      durationpb.New(10 * time.Millisecond),
					HealthChecker: &core.HealthCheck_NoHealthCheck{NoHealthCheck: &core.NoHealthCheck{}},
				},
			})).To(Succeed())
		}
		resolvers := make([]*Resolver, 0, len(joint.resolversCached))
		for _, state := range joint.resolversCached {
			resolvers = append(resolvers, state.resolver)
		}

		Expect(joint.Stop()).To(Succeed())

		Expect(resolvers).To(HaveLen(2))
		for _, resolver := range resolvers {
			Expect(resolver.Done()).To(BeClosed())
		}
	})
})
