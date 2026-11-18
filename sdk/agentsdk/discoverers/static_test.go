package discoverers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/sdk/agentsdk/discoverers"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
)

var _ = Describe("Static", func() {
	Context("Run", func() {
		It("should return targets", func() {
			targets := []*core.IPAddr{
				{
					Ip:   "127.0.0.1",
					Port: uint32(8080),
				},
				{
					Ip:   "127.0.0.1",
					Port: uint32(8081),
				},
			}
			sd := NewStatic(targets)

			found := runDiscovery(sd)
			Expect(<-found).To(Equal(targets))
		})
	})
})
