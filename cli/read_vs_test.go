package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
)

var _ = Describe("virtual-server", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcvsconnect.VirtualServerReadVSProcedure, reply(&svcvs.VSResponse{Vs: &vs.VirtualServer{UUID: resourceUuid, Name: "shown"}}))
	})

	It("prints the virtual server configuration", func() {
		out, err := runner.run("virtual-server", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Virtual Server " + resourceUuid + " configuration"))
		Expect(out).To(ContainSubstring("name: shown"))
	})

	It("prints only the uuid with --uuid", func() {
		out, err := runner.run("virtual-server", "--uuid", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(resourceUuid + "\n"))
	})

	It("prints json with --json", func() {
		out, err := runner.run("virtual-server", "--json", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).To(HaveKeyWithValue("name", "shown"))
	})
})
