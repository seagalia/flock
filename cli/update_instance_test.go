package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
)

var _ = Describe("update-instance", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcinstanceconnect.InstanceReadInstanceProcedure, reply(&svcinstance.InstanceResponse{Instance: &instance.Instance{UUID: resourceUuid, Name: "lb"}}))
	})

	It("sends the file content with the uuid of the current instance", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceUpdateInstanceProcedure, reply(&svcinstance.InstanceResponse{}))

		out, err := runner.run("update-instance", "--force", "--file", writeTempFile("instance.yml", "name: renamed"), resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("updated from file"))
		Expect(lastRequest[*svccore.UuidRequest](runner.conn, svcinstanceconnect.InstanceReadInstanceProcedure).GetUUID()).To(Equal(resourceUuid))
		sent := lastRequest[*svcinstance.UpdateInstanceRequest](runner.conn, svcinstanceconnect.InstanceUpdateInstanceProcedure)
		Expect(sent.GetUUID()).To(Equal(resourceUuid))
		Expect(sent.GetInstance().GetName()).To(Equal("renamed"))
		Expect(sent.GetInstance().GetUUID()).To(Equal(resourceUuid))
	})

	It("fails on an empty file without updating", func() {
		_, err := runner.run("update-instance", "--force", "--file", writeTempFile("instance.yml", ""), resourceUuid)

		Expect(err).To(MatchError(ContainSubstring("is empty")))
		Expect(runner.conn.Requests(svcinstanceconnect.InstanceUpdateInstanceProcedure)).To(BeEmpty())
	})
})
