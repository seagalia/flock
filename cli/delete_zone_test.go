package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("delete-zone", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svczoneconnect.ZoneListZoneProcedure, reply(&svczone.ListZoneResponse{Zones: []*zone.Zone{{UUID: zoneUuid, Name: "production"}}}))
		runner.conn.Handle(svczoneconnect.ZoneDeleteZoneProcedure, reply(&emptypb.Empty{}))
	})

	It("deletes the zone found by its name with its uuid", func() {
		out, err := runner.run("delete-zone", "--force", "production")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Zone successfully deleted"))
		Expect(lastRequest[*svccore.UuidRequest](runner.conn, svczoneconnect.ZoneDeleteZoneProcedure).GetUUID()).To(Equal(zoneUuid))
	})

	It("is reachable through its alias", func() {
		_, err := runner.run("dzone", "--force", "production")

		Expect(err).NotTo(HaveOccurred())
		Expect(runner.conn.Requests(svczoneconnect.ZoneDeleteZoneProcedure)).To(HaveLen(1))
	})
})
