package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
)

var _ = Describe("backend", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcbackendconnect.BackendReadBackendProcedure, reply(&svcbackend.BackendResponse{Backend: &backend.Backend{UUID: resourceUuid, Name: "shown"}}))
	})

	It("prints the backend configuration", func() {
		out, err := runner.run("backend", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Backend " + resourceUuid + " configuration"))
		Expect(out).To(ContainSubstring("name: shown"))
	})

	It("prints only the uuid with --uuid", func() {
		out, err := runner.run("backend", "--uuid", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(resourceUuid + "\n"))
	})

	It("prints json with --json", func() {
		out, err := runner.run("backend", "--json", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).To(HaveKeyWithValue("name", "shown"))
	})
})
