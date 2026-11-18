package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
)

var _ = Describe("agents", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("lists the agents of an instance found by name", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, reply(&svcinstance.ListInstanceResponse{
			Instances: []*instance.Instance{{UUID: resourceUuid, Name: "lb"}},
		}))
		runner.conn.Handle(svclookupconnect.LookupListAgentProcedure, reply(&svclookup.ListAgentResponse{Agents: []*agent.Agent{{
			UUID:       "agent-1",
			Host:       "host-1",
			Type:       "starling",
			Extensions: []string{"l3logs"},
			Features:   &agent.AgentFeatures{ShowLogs: true},
		}, {UUID: "acme-1", Host: "host-2", Mode: agent.Agent_PROVISIONER}}}))

		out, err := runner.run("agents", "lb")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("agent-1"))
		Expect(out).To(ContainSubstring("host-1"))
		Expect(out).To(ContainSubstring("starling"))
		Expect(out).To(ContainSubstring("l3logs"))
		Expect(out).To(ContainSubstring("MODE"))
		Expect(out).To(ContainSubstring("SIDECAR"))
		Expect(out).To(ContainSubstring("PROVISIONER"))
		Expect(out).To(ContainSubstring("Show logs: Yes"))
		Expect(out).To(ContainSubstring("Show metrics: No"))
		sent := lastRequest[*svclookup.ListAgentRequest](runner.conn, svclookupconnect.LookupListAgentProcedure)
		Expect(sent.GetInstanceUuid()).To(Equal(resourceUuid))
	})

	It("says when the instance has no agent", func() {
		runner.conn.Handle(svclookupconnect.LookupListAgentProcedure, reply(&svclookup.ListAgentResponse{}))

		out, err := runner.run("a", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("No agent found."))
	})
})
