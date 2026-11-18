package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
)

var _ = Describe("zones", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("prints every zone with its pools, marking the default one", func() {
		runner.conn.Handle(svczoneconnect.ZoneListZoneProcedure, firstPage(&svczone.ListZoneResponse{
			Zones: []*zone.Zone{{
				Name: "production",
				Pools: []*zone.IpPool{
					{Name: "internal", Privacy: zone.IpPool_PRIVATE, Subnets: []*zone.Subnet{{Ip: "10.0.0.0", PrefixLength: 24}}},
					{Name: "edge", IpRanges: []*zone.IpRange{{IpMin: "10.1.0.10", IpMax: "10.1.0.20"}}},
				},
			}},
		}))

		out, err := runner.run("zones")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("production"))
		Expect(out).To(ContainSubstring("internal, edge (default)"))
	})

	It("says when there is no zone", func() {
		runner.conn.Handle(svczoneconnect.ZoneListZoneProcedure, firstPage(&svczone.ListZoneResponse{}))

		out, err := runner.run("zones")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("No Zones found."))
	})

	It("sends metadata filters given with --filter", func() {
		runner.conn.Handle(svczoneconnect.ZoneListZoneProcedure, firstPage(&svczone.ListZoneResponse{}))

		_, err := runner.run("zones", "--filter", "env==prod")

		Expect(err).NotTo(HaveOccurred())
		Expect(sentFilters(runner, svczoneconnect.ZoneListZoneProcedure)).To(ConsistOf(HaveField("Key", "env")))
	})
})
