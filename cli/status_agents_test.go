package cli

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
)

var _ = Describe("status-agents", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcinstanceconnect.InstanceReadInstanceProcedure, reply(&svcinstance.InstanceResponse{
			Instance: &instance.Instance{UUID: resourceUuid, Name: "lb"},
		}))
		runner.conn.Handle(svclookupconnect.LookupListAgentProcedure, reply(&svclookup.ListAgentResponse{
			Agents: []*agent.Agent{{UUID: "agent-1"}, {UUID: "acme-1", Mode: agent.Agent_PROVISIONER}},
		}))
	})

	It("prints the provisioners last, marked (provisioner)", func() {
		sidecar := func(uuid string) *svclookup.GetAgentStatusResponse {
			return &svclookup.GetAgentStatusResponse{
				AgentUuid: uuid,
				AgentStatus: &agent.AgentStatus{State: &agent.AgentStatus_SidecarAgentState{SidecarAgentState: &agent.SidecarAgentState{
					AgentState: &agent.State{Status: agent.Status_OK},
				}}},
			}
		}
		runner.conn.Handle(svclookupconnect.LookupListAgentStatusProcedure, reply(&svclookup.ListAgentStatusResponse{
			AgentStatus: []*svclookup.GetAgentStatusResponse{sidecar("acme-1"), sidecar("agent-1")},
		}))

		out, err := runner.run("status-agents", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("acme-1 (provisioner)"))
		Expect(out).NotTo(ContainSubstring("agent-1 (provisioner)"))
		Expect(strings.Index(out, "agent-1")).To(BeNumerically("<", strings.Index(out, "acme-1")))
	})

	It("shows the state of sidecar agents, their components and announced vips", func() {
		runner.conn.Handle(svclookupconnect.LookupListAgentStatusProcedure, reply(&svclookup.ListAgentStatusResponse{
			AgentStatus: []*svclookup.GetAgentStatusResponse{{
				AgentUuid: "agent-1",
				AgentStatus: &agent.AgentStatus{State: &agent.AgentStatus_SidecarAgentState{SidecarAgentState: &agent.SidecarAgentState{
					AgentState:      &agent.State{Status: agent.Status_OK, Details: "all good"},
					ComponentStates: []*agent.State{{Name: "envoy", Status: agent.Status_WARNING, Details: "slow"}},
					AnnouncingVips:  []*vip.VIP{{Ip: "10.0.0.1"}},
				}}},
			}},
		}))

		out, err := runner.run("status-agents", "--show-announcing-vips", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("agent-1"))
		Expect(out).To(ContainSubstring("all good"))
		Expect(out).To(ContainSubstring("envoy"))
		Expect(out).To(ContainSubstring("WARNING"))
		Expect(out).To(ContainSubstring("10.0.0.1/32"))
		sent := lastRequest[*svclookup.ListAgentStatusRequest](runner.conn, svclookupconnect.LookupListAgentStatusProcedure)
		Expect(sent.GetInstanceUuid()).To(Equal(resourceUuid))
		Expect(sent.GetShowAnnouncingVips()).To(BeTrue())
	})

	It("shows the components of proxy agents", func() {
		runner.conn.Handle(svclookupconnect.LookupListAgentStatusProcedure, reply(&svclookup.ListAgentStatusResponse{
			AgentStatus: []*svclookup.GetAgentStatusResponse{{
				AgentUuid: "agent-1",
				AgentStatus: &agent.AgentStatus{State: &agent.AgentStatus_ProxyAgentState{ProxyAgentState: &agent.BridgeAgentStatus{
					ComponentStates: []*agent.State{{Name: "server-a", Status: agent.Status_ERRORED, Details: "crashed"}},
				}}},
			}},
		}))

		out, err := runner.run("status-agents", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("server-a"))
		Expect(out).To(ContainSubstring("ERRORED"))
		Expect(out).To(ContainSubstring("crashed"))
	})

	It("shows the provisioners of a bridge instance after its components, with no vip", func() {
		runner.conn.Handle(svclookupconnect.LookupListAgentStatusProcedure, reply(&svclookup.ListAgentStatusResponse{
			AgentStatus: []*svclookup.GetAgentStatusResponse{
				{
					AgentUuid: "agent-1",
					AgentStatus: &agent.AgentStatus{State: &agent.AgentStatus_ProxyAgentState{ProxyAgentState: &agent.BridgeAgentStatus{
						ComponentStates: []*agent.State{{Name: "server-a", Status: agent.Status_OK}},
						ComponentAnnouncingVips: []*agent.ComponentAnnouncingVips{
							{Name: "server-a", AnnouncingVips: []*vip.VIP{{Ip: "10.0.0.1"}}},
						},
					}}},
				},
				{
					AgentUuid: "acme-1",
					AgentStatus: &agent.AgentStatus{State: &agent.AgentStatus_SidecarAgentState{SidecarAgentState: &agent.SidecarAgentState{
						AgentState:      &agent.State{Status: agent.Status_OK},
						ComponentStates: []*agent.State{{Name: "acme", Status: agent.Status_ERRORED, Details: "rate limited"}},
					}}},
				},
			},
		}))

		out, err := runner.run("status-agents", "--show-announcing-vips", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("acme-1 (provisioner)"))
		Expect(out).To(ContainSubstring("rate limited"))
		Expect(strings.Index(out, "server-a")).To(BeNumerically("<", strings.Index(out, "acme-1")))
		vipsTable := out[strings.Index(out, "Announcing VIPs"):]
		Expect(vipsTable).To(ContainSubstring("10.0.0.1/32"))
		Expect(vipsTable).NotTo(ContainSubstring("acme-1"))
	})

	It("fails without agents", func() {
		runner.conn.Handle(svclookupconnect.LookupListAgentStatusProcedure, reply(&svclookup.ListAgentStatusResponse{}))

		_, err := runner.run("status-agents", resourceUuid)

		Expect(err).To(MatchError("No agent found"))
	})

	It("fails on an unknown agent type", func() {
		runner.conn.Handle(svclookupconnect.LookupListAgentStatusProcedure, reply(&svclookup.ListAgentStatusResponse{
			AgentStatus: []*svclookup.GetAgentStatusResponse{{AgentUuid: "agent-1"}},
		}))

		_, err := runner.run("status-agents", resourceUuid)

		Expect(err).To(MatchError("unknown agent type"))
	})
})
