package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
)

var _ = Describe("show-instance-status", func() {
	It("shows current and desired state of the instance", func() {
		runner := newCommandRunner()
		runner.conn.Handle(svcinstanceconnect.InstanceGetInstanceStatusProcedure, reply(&svcinstance.GetInstanceStatusResponse{
			Status: &instance.InstanceStatus{
				CurrentState: instance.InstanceStatus_CREATING,
				DesiredState: instance.InstanceStatus_RUNNING,
				ErrorMessage: "waiting for agents",
			},
		}))

		out, err := runner.run("sis", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("CREATING"))
		Expect(out).To(ContainSubstring("RUNNING"))
		Expect(out).To(ContainSubstring("waiting for agents"))
		sent := lastRequest[*svccore.UuidRequest](runner.conn, svcinstanceconnect.InstanceGetInstanceStatusProcedure)
		Expect(sent.GetUUID()).To(Equal(resourceUuid))
	})
})
