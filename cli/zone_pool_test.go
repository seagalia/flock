package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
)

var _ = Describe("zone pools commands", func() {
	var runner *commandRunner

	const poolFile = "name: ns-a\nprivacy: PRIVATE\nsubnets:\n- ip: 10.0.0.0\n  prefix_length: 24\n"

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svczoneconnect.ZoneListZoneProcedure, reply(&svczone.ListZoneResponse{Zones: []*zone.Zone{{
			UUID: zoneUuid,
			Name: "production",
			Pools: []*zone.IpPool{
				{Name: "ns-a", Privacy: zone.IpPool_PUBLIC},
				{Name: "ns-b", Privacy: zone.IpPool_PUBLIC},
			},
		}}}))
	})

	Describe("add-pool-to-zone", func() {
		It("sends the pool of the file with the uuid of the zone found by its name", func() {
			runner.conn.Handle(svczoneconnect.ZoneAddPoolToZoneProcedure, reply(&svczone.ZoneResponse{}))

			out, err := runner.run("add-pool-to-zone", "--force", "--file", writeTempFile("pool.yml", poolFile), "production")

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("added"))
			sent := lastRequest[*svczone.AddPoolToZoneRequest](runner.conn, svczoneconnect.ZoneAddPoolToZoneProcedure)
			Expect(sent.GetUUID()).To(Equal(zoneUuid))
			Expect(sent.GetPool().GetName()).To(Equal("ns-a"))
			Expect(sent.GetPool().GetPrivacy()).To(Equal(zone.IpPool_PRIVATE))
			Expect(sent.GetPool().GetSubnets()).To(HaveLen(1))
		})

		It("fails on an empty file without adding", func() {
			_, err := runner.run("add-pool-to-zone", "--force", "--file", writeTempFile("pool.yml", ""), "production")

			Expect(err).To(MatchError(ContainSubstring("is empty")))
			Expect(runner.conn.Requests(svczoneconnect.ZoneAddPoolToZoneProcedure)).To(BeEmpty())
		})
	})

	Describe("update-pool-of-zone", func() {
		It("sends the pool of the file, matched by its name", func() {
			runner.conn.Handle(svczoneconnect.ZoneUpdatePoolOfZoneProcedure, reply(&svczone.ZoneResponse{}))

			out, err := runner.run("update-pool-of-zone", "--force", "--file", writeTempFile("pool.yml", poolFile), "production")

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("updated"))
			sent := lastRequest[*svczone.UpdatePoolOfZoneRequest](runner.conn, svczoneconnect.ZoneUpdatePoolOfZoneProcedure)
			Expect(sent.GetUUID()).To(Equal(zoneUuid))
			Expect(sent.GetPool().GetName()).To(Equal("ns-a"))
			Expect(sent.GetPool().GetPrivacy()).To(Equal(zone.IpPool_PRIVATE))
		})

		It("fails when the zone has no pool of that name", func() {
			_, err := runner.run("update-pool-of-zone", "--force", "--file", writeTempFile("pool.yml", "name: unknown"), "production")

			Expect(err).To(MatchError(ContainSubstring("unknown")))
			Expect(runner.conn.Requests(svczoneconnect.ZoneUpdatePoolOfZoneProcedure)).To(BeEmpty())
		})
	})

	Describe("delete-pool-from-zone", func() {
		It("sends the uuid of the zone and the name of the pool", func() {
			runner.conn.Handle(svczoneconnect.ZoneDeletePoolFromZoneProcedure, reply(&svczone.ZoneResponse{}))

			out, err := runner.run("delete-pool-from-zone", "--force", "production", "ns-b")

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("deleted"))
			sent := lastRequest[*svczone.DeletePoolFromZoneRequest](runner.conn, svczoneconnect.ZoneDeletePoolFromZoneProcedure)
			Expect(sent.GetUUID()).To(Equal(zoneUuid))
			Expect(sent.GetPoolName()).To(Equal("ns-b"))
		})

		It("fails when the zone has no pool of that name", func() {
			_, err := runner.run("delete-pool-from-zone", "--force", "production", "unknown")

			Expect(err).To(MatchError(ContainSubstring("unknown")))
			Expect(runner.conn.Requests(svczoneconnect.ZoneDeletePoolFromZoneProcedure)).To(BeEmpty())
		})
	})
})
