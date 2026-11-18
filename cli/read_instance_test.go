package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
)

var _ = Describe("instance", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcinstanceconnect.InstanceReadInstanceProcedure, reply(&svcinstance.InstanceResponse{Instance: &instance.Instance{UUID: resourceUuid, Name: "shown"}}))
	})

	It("prints the instance configuration", func() {
		out, err := runner.run("instance", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Instance " + resourceUuid + " configuration"))
		Expect(out).To(ContainSubstring("name: shown"))
	})

	It("prints only the uuid with --uuid", func() {
		out, err := runner.run("instance", "--uuid", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(resourceUuid + "\n"))
	})

	It("prints json with --json", func() {
		out, err := runner.run("instance", "--json", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).To(HaveKeyWithValue("name", "shown"))
	})
})
