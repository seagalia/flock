package cli

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
)

var _ = Describe("create-zone", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("sends the zone from the file", func() {
		runner.conn.Handle(svczoneconnect.ZoneCreateZoneProcedure, reply(&svczone.ZoneResponse{}))

		out, err := runner.run("create-zone", "--force", "--file", writeTempFile("zone.yml", "name: production\nzone: z1"))

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Zone created successfully"))
		sent := lastRequest[*svczone.CreateZoneRequest](runner.conn, svczoneconnect.ZoneCreateZoneProcedure).GetZone()
		Expect(sent.GetName()).To(Equal("production"))
		Expect(sent.GetZone()).To(Equal("z1"))
	})

	It("writes a skeleton file with --init without calling the server", func() {
		path := filepath.Join(GinkgoT().TempDir(), "zone.yml")

		out, err := runner.run("create-zone", "--init", "--file", path)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Initialized json file"))
		Expect(path).To(BeAnExistingFile())
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("fails on an empty file", func() {
		_, err := runner.run("create-zone", "--force", "--file", writeTempFile("zone.yml", ""))

		Expect(err).To(MatchError(ContainSubstring("is empty")))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})
})
