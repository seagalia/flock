package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
)

var _ = Describe("update-vs", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcvsconnect.VirtualServerReadVSProcedure, reply(&svcvs.VSResponse{Vs: &vs.VirtualServer{UUID: resourceUuid, Name: "web"}}))
	})

	It("sends the file content with the uuid of the current virtual server", func() {
		runner.conn.Handle(svcvsconnect.VirtualServerUpdateVSProcedure, reply(&svcvs.VSResponse{}))

		out, err := runner.run("update-vs", "--force", "--file", writeTempFile("vs.yml", "name: renamed"), resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("updated from file"))
		Expect(lastRequest[*svccore.UuidRequest](runner.conn, svcvsconnect.VirtualServerReadVSProcedure).GetUUID()).To(Equal(resourceUuid))
		sent := lastRequest[*svcvs.UpdateVSRequest](runner.conn, svcvsconnect.VirtualServerUpdateVSProcedure)
		Expect(sent.GetUUID()).To(Equal(resourceUuid))
		Expect(sent.GetVs().GetName()).To(Equal("renamed"))
		Expect(sent.GetVs().GetUUID()).To(Equal(resourceUuid))
	})

	It("fails on an empty file without updating", func() {
		_, err := runner.run("update-vs", "--force", "--file", writeTempFile("vs.yml", ""), resourceUuid)

		Expect(err).To(MatchError(ContainSubstring("is empty")))
		Expect(runner.conn.Requests(svcvsconnect.VirtualServerUpdateVSProcedure)).To(BeEmpty())
	})
})
