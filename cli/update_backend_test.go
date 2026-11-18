package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

var _ = Describe("update-backend", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcbackendconnect.BackendReadBackendProcedure, reply(&svcbackend.BackendResponse{Backend: &backend.Backend{UUID: resourceUuid, Name: "backend"}}))
	})

	It("sends the file content with the uuid of the current backend", func() {
		runner.conn.Handle(svcbackendconnect.BackendUpdateBackendProcedure, reply(&svcbackend.BackendResponse{}))

		out, err := runner.run("update-backend", "--force", "--file", writeTempFile("backend.yml", "name: renamed"), resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("updated from file"))
		Expect(lastRequest[*svccore.UuidRequest](runner.conn, svcbackendconnect.BackendReadBackendProcedure).GetUUID()).To(Equal(resourceUuid))
		sent := lastRequest[*svcbackend.UpdateBackendRequest](runner.conn, svcbackendconnect.BackendUpdateBackendProcedure)
		Expect(sent.GetUUID()).To(Equal(resourceUuid))
		Expect(sent.GetBackend().GetName()).To(Equal("renamed"))
		Expect(sent.GetBackend().GetUUID()).To(Equal(resourceUuid))
	})

	It("fails on an empty file without updating", func() {
		_, err := runner.run("update-backend", "--force", "--file", writeTempFile("backend.yml", ""), resourceUuid)

		Expect(err).To(MatchError(ContainSubstring("is empty")))
		Expect(runner.conn.Requests(svcbackendconnect.BackendUpdateBackendProcedure)).To(BeEmpty())
	})
})
