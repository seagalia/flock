package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
)

// zoneUuid is how the cli recognizes a zone given by uuid rather than by name
const zoneUuid = "11111111-2222-3333-4444-555555555555"

var _ = Describe("zone", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		production := &zone.Zone{UUID: zoneUuid, Name: "production", Zone: "z1"}
		runner.conn.Handle(svczoneconnect.ZoneReadZoneProcedure, reply(&svczone.ZoneResponse{Zone: production}))
		runner.conn.Handle(svczoneconnect.ZoneListZoneProcedure, reply(&svczone.ListZoneResponse{Zones: []*zone.Zone{production}}))
	})

	It("prints the zone configuration", func() {
		out, err := runner.run("zone", zoneUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("configuration"))
		Expect(out).To(ContainSubstring("zone: z1"))
	})

	It("finds the zone by its name", func() {
		out, err := runner.run("zone", "--json", "production")

		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).To(HaveKeyWithValue("UUID", zoneUuid))
		sent := lastRequest[*svczone.ListZoneRequest](runner.conn, svczoneconnect.ZoneListZoneProcedure)
		Expect(sent.GetFilters()[0].GetValue()).To(Equal("production"))
	})
})
