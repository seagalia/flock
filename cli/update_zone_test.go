package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
)

var _ = Describe("update-zone", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svczoneconnect.ZoneReadZoneProcedure, reply(&svczone.ZoneResponse{Zone: &zone.Zone{UUID: zoneUuid, Name: "production", Zone: "z1"}}))
	})

	It("updates the current zone by its uuid", func() {
		runner.conn.Handle(svczoneconnect.ZoneUpdateZoneProcedure, reply(&svczone.ZoneResponse{}))

		out, err := runner.run("update-zone", "--force", "--file", writeTempFile("zone.yml", "name: renamed\nzone: z2"), zoneUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("updated from file"))
		sent := lastRequest[*svczone.UpdateZoneRequest](runner.conn, svczoneconnect.ZoneUpdateZoneProcedure)
		Expect(sent.GetUUID()).To(Equal(zoneUuid))
		Expect(sent.GetZone().GetUUID()).To(Equal(zoneUuid))
		Expect(sent.GetZone().GetName()).To(Equal("renamed"))
		Expect(sent.GetZone().GetZone()).To(Equal("z2"))
	})

	It("fails on an empty file without updating", func() {
		_, err := runner.run("update-zone", "--force", "--file", writeTempFile("zone.yml", ""), zoneUuid)

		Expect(err).To(MatchError(ContainSubstring("is empty")))
		Expect(runner.conn.Requests(svczoneconnect.ZoneUpdateZoneProcedure)).To(BeEmpty())
	})
})
