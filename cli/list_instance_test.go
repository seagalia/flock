package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
)

var _ = Describe("instances", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("prints every instance with its zone", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, firstPage(&svcinstance.ListInstanceResponse{
			Instances: []*instance.Instance{{Name: "lb", ZoneUuid: "production"}},
		}))

		out, err := runner.run("instances")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("lb"))
		Expect(out).To(ContainSubstring("production"))
	})

	It("shows uuids with --show-uuid", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, firstPage(&svcinstance.ListInstanceResponse{
			Instances: []*instance.Instance{{UUID: resourceUuid, Name: "lb"}},
		}))

		out, err := runner.run("i", "--show-uuid")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring(resourceUuid))
	})

	It("says when there is no instance", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, firstPage(&svcinstance.ListInstanceResponse{}))

		out, err := runner.run("instances")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("No instances found."))
	})

	It("sends metadata filters given with --filter", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, firstPage(&svcinstance.ListInstanceResponse{}))

		_, err := runner.run("instances", "--filter", "env==prod")

		Expect(err).NotTo(HaveOccurred())
		Expect(sentFilters(runner, svcinstanceconnect.InstanceListInstanceProcedure)).To(ConsistOf(HaveField("Key", "env")))
	})
})
