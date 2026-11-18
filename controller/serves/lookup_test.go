package serves_test

import (
	"context"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/controller/lockers"
	"github.com/seagalia/flock/controller/permissioners"
	. "github.com/seagalia/flock/controller/serves"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	agentlogs "github.com/seagalia/flock/sdk/flock/services/agent/logs/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1/agentstatusconnect"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeResourceStatus struct {
	agentstatusconnect.UnimplementedResourceStatusHandler
}

func (fakeResourceStatus) GetStatus(context.Context, *agentstatus.GetStatusRequest) (*agentstatus.StatusResponse, error) {
	return &agentstatus.StatusResponse{Error: "from reachable agent", Notes: []*flocktype.Note{{Message: "approximated"}}}, nil
}

func (fakeResourceStatus) ListStatus(context.Context, *agentstatus.ListStatusRequest) (*agentstatus.ListStatusResponse, error) {
	return &agentstatus.ListStatusResponse{Statuses: []*agentstatus.StatusResponse{{Notes: []*flocktype.Note{{Message: "approximated"}}}}}, nil
}

// labelledAgent serves the agent services and tells who answered through the log line, the status error and the agent
// status name
type labelledAgent struct {
	agentlogsconnect.UnimplementedLogsHandler
	agentstatusconnect.UnimplementedResourceStatusHandler
	agentstatusconnect.UnimplementedAgentStatusHandler
	label string
}

func (l labelledAgent) Latest(context.Context, *emptypb.Empty) (*agentlogs.LogLatestResponse, error) {
	return &agentlogs.LogLatestResponse{LogLines: []string{l.label}}, nil
}

func (l labelledAgent) GetStatus(context.Context, *agentstatus.GetStatusRequest) (*agentstatus.StatusResponse, error) {
	return &agentstatus.StatusResponse{Error: l.label}, nil
}

func (l labelledAgent) ListStatus(context.Context, *agentstatus.ListStatusRequest) (*agentstatus.ListStatusResponse, error) {
	return &agentstatus.ListStatusResponse{Statuses: []*agentstatus.StatusResponse{{Error: l.label}}}, nil
}

func (l labelledAgent) ListResourceTypes(context.Context, *emptypb.Empty) (*agentstatus.ListResourceTypesResponse, error) {
	return &agentstatus.ListResourceTypesResponse{ResourceTypes: []string{l.label + "-type"}}, nil
}

func (l labelledAgent) GetAgentStatus(context.Context, *agentstatus.GetAgentStatusRequest) (*agent.AgentStatus, error) {
	return &agent.AgentStatus{State: &agent.AgentStatus_SidecarAgentState{
		SidecarAgentState: &agent.SidecarAgentState{AgentState: &agent.State{Name: l.label}},
	}}, nil
}

// silentAgent never answers GetAgentStatus until release is closed
type silentAgent struct {
	agentstatusconnect.UnimplementedAgentStatusHandler
	release chan struct{}
}

func (s silentAgent) GetAgentStatus(context.Context, *agentstatus.GetAgentStatusRequest) (*agent.AgentStatus, error) {
	<-s.release
	return &agent.AgentStatus{}, nil
}

func newLookupServer() *FlockServer {
	serverTls, clientTls := rpctest.NewTLS("controller")
	agentServer := connect.NewServer()
	agentstatusconnect.RegisterResourceStatusHandler(agentServer, fakeResourceStatus{})
	agentAddr, stopAgent := rpctest.Serve(agentServer, serverTls)
	DeferCleanup(stopAgent)

	inst := newFakeInstantiater()
	inst.agents = []*agent.Agent{
		{UUID: "agent-up", Host: agentAddr, Features: &agent.AgentFeatures{ShowStatus: true}},
	}
	perm := permissioners.NewMetrics(permissioners.NewDbStorePerm(dbStoreMan))
	admConf := config.AdminConfig{Users: []string{adminUser}}
	return NewFlockServer(context.Background(), clientTls, admConf, nil, dbStoreMan, nil, nil, perm, inst, nil, lockers.NewMemory(), nil)
}

var _ = Describe("Lookup", func() {
	Context("GetStatus", func() {
		It("should skip an agent that cannot be reached", func() {
			serverTls, clientTls := rpctest.NewTLS("controller")
			agentServer := connect.NewServer()
			agentstatusconnect.RegisterResourceStatusHandler(agentServer, fakeResourceStatus{})
			agentAddr, stopAgent := rpctest.Serve(agentServer, serverTls)
			DeferCleanup(stopAgent)

			inst := newFakeInstantiater()
			inst.agents = []*agent.Agent{
				{UUID: "agent-down", Host: "127.0.0.1:1", Features: &agent.AgentFeatures{ShowStatus: true}},
				{UUID: "agent-up", Host: agentAddr, Features: &agent.AgentFeatures{ShowStatus: true}},
			}
			perm := permissioners.NewMetrics(permissioners.NewDbStorePerm(dbStoreMan))
			admConf := config.AdminConfig{Users: []string{adminUser}}
			server := NewFlockServer(context.Background(), clientTls, admConf, nil, dbStoreMan, nil, nil, perm, inst, nil, lockers.NewMemory(), nil)

			// the agents are tried in map order, so agent-down is the first one in about half of the calls
			for range 20 {
				resp, err := server.GetStatus(userCtx(adminUser), &svclookup.GetStatusRequest{
					InstanceUuid: "inst-1", TypeUrl: "type", Identifier: "id",
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetError()).To(Equal("from reachable agent"))
			}
		})

		It("should give the notes of the agent", func() {
			resp, err := newLookupServer().GetStatus(userCtx(adminUser), &svclookup.GetStatusRequest{
				InstanceUuid: "inst-1", TypeUrl: "type", Identifier: "id",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetNotes()).To(ConsistOf(HaveField("Message", "approximated")))
		})
	})

	Context("ListAgentStatus with an agent that never answers", func() {
		It("should return within the agent call timeout with the agent in error", func() {
			DeferCleanup(ShortenAgentCallTimeout(500 * time.Millisecond))
			serverTls, clientTls := rpctest.NewTLS("controller")
			release := make(chan struct{})
			agentServer := connect.NewServer()
			agentstatusconnect.RegisterAgentStatusHandler(agentServer, silentAgent{release: release})
			agentAddr, stopAgent := rpctest.Serve(agentServer, serverTls)
			DeferCleanup(stopAgent)
			// runs before stopAgent so the blocked handler can end
			DeferCleanup(func() { close(release) })

			inst := newFakeInstantiater()
			inst.agents = []*agent.Agent{
				{UUID: "agent-mute", Name: "agent-mute", Host: agentAddr, Features: &agent.AgentFeatures{ShowStatus: true}},
			}
			perm := permissioners.NewMetrics(permissioners.NewDbStorePerm(dbStoreMan))
			admConf := config.AdminConfig{Users: []string{adminUser}}
			server := NewFlockServer(context.Background(), clientTls, admConf, nil, dbStoreMan, nil, nil, perm, inst, nil, lockers.NewMemory(), nil)

			start := time.Now()
			resp, err := server.ListAgentStatus(userCtx(adminUser), &svclookup.ListAgentStatusRequest{InstanceUuid: "inst-1"})

			Expect(err).ToNot(HaveOccurred())
			Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
			Expect(resp.GetAgentStatus()).To(HaveLen(1))
			Expect(resp.GetAgentStatus()[0].GetAgentStatus().GetSidecarAgentState().GetAgentState().GetStatus()).
				To(Equal(agent.Status_ERRORED))
		})
	})

	Context("ListStatus", func() {
		It("should give the notes of the agent", func() {
			resp, err := newLookupServer().ListStatus(userCtx(adminUser), &svclookup.ListStatusRequest{
				InstanceUuid: "inst-1", TypeUrl: "type",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetStatus()).To(HaveLen(1))
			Expect(resp.GetStatus()[0].GetNotes()).To(ConsistOf(HaveField("Message", "approximated")))
		})
	})

	Context("ListInstanceNodes", func() {
		var server *testServer
		BeforeEach(func() {
			server = newTestServer(newFakeInstantiater())
			Expect(dbStoreMan.Instances().Create(context.Background(), &instance.Instance{UUID: "inst-1", Name: "inst-1"})).To(Succeed())
			node := &svclookup.InstanceNode{Node: &core.Node{}, AgentUuid: "agent-a", Name: "node-a"}
			Expect(dbStoreMan.Nodes().ClaimNode(context.Background(), "inst-1", node, "10.0.0.1", 90*time.Second)).To(Succeed())
		})
		It("should list the nodes of the instance for a reader", func() {
			Expect(server.perm.AddPermission(context.Background(), helpers.TypeUrl[*instance.Instance](), "inst-1",
				permission.Role_READER, permission.MemberType_USER, "bob")).To(Succeed())

			resp, err := server.ListInstanceNodes(userCtx("bob"), &svclookup.ListInstanceNodesRequest{InstanceUuid: "inst-1"})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetNodes()).To(HaveLen(1))
			Expect(resp.GetNodes()[0].GetName()).To(Equal("node-a"))
			Expect(resp.GetNodes()[0].GetAgentUuid()).To(Equal("agent-a"))
		})
		It("should be refused without the reader role on the instance", func() {
			_, err := server.ListInstanceNodes(userCtx("bob"), &svclookup.ListInstanceNodesRequest{InstanceUuid: "inst-1"})

			expectCode(err, connect.CodePermissionDenied)
		})
	})

	Context("provisioners", func() {
		var (
			server    *testServer
			agentList []*agent.Agent
		)
		allFeatures := &agent.AgentFeatures{ShowLogs: true, ShowStatus: true}

		BeforeEach(func() {
			serverTls, clientTls := rpctest.NewTLS("controller")
			serve := func(label string) string {
				agentServer := connect.NewServer()
				agentlogsconnect.RegisterLogsHandler(agentServer, labelledAgent{label: label})
				agentstatusconnect.RegisterResourceStatusHandler(agentServer, labelledAgent{label: label})
				agentstatusconnect.RegisterAgentStatusHandler(agentServer, labelledAgent{label: label})
				addr, stop := rpctest.Serve(agentServer, serverTls)
				DeferCleanup(stop)
				return addr
			}
			agentList = []*agent.Agent{
				{UUID: "a-prov", Name: "a-prov", Mode: agent.Agent_PROVISIONER, Host: serve("a-prov"), Features: allFeatures},
				{UUID: "b-bridge", Name: "b-bridge", Mode: agent.Agent_BRIDGE, Host: serve("b-bridge"), Features: allFeatures},
				{UUID: "c-bridge", Name: "c-bridge", Mode: agent.Agent_BRIDGE, Host: serve("c-bridge"), Features: allFeatures},
			}
			inst := newFakeInstantiater()
			inst.agents = agentList
			perm := permissioners.NewMetrics(permissioners.NewDbStorePerm(dbStoreMan))
			admConf := config.AdminConfig{Users: []string{adminUser}}
			flock := NewFlockServer(context.Background(), clientTls, admConf, nil, dbStoreMan, nil, nil, perm, inst, nil, lockers.NewMemory(), nil)
			server = &testServer{FlockServer: flock, perm: perm}
		})

		It("should ask one bridge agent and the provisioner for the latest logs", func() {
			resp, err := server.GetLogsLatest(userCtx(adminUser), &svclookup.GetLogsRequest{InstanceUuid: "inst-1"})

			Expect(err).ToNot(HaveOccurred())
			asked := make([]string, 0, len(resp.GetLogs()))
			for _, logs := range resp.GetLogs() {
				asked = append(asked, logs.GetAgentUuid())
			}
			Expect(asked).To(ConsistOf("b-bridge", "a-prov"))
		})

		It("should ask one bridge agent and the provisioner for the agent status", func() {
			resp, err := server.ListAgentStatus(userCtx(adminUser), &svclookup.ListAgentStatusRequest{InstanceUuid: "inst-1"})

			Expect(err).ToNot(HaveOccurred())
			asked := make([]string, 0, len(resp.GetAgentStatus()))
			for _, status := range resp.GetAgentStatus() {
				asked = append(asked, status.GetAgentUuid())
			}
			Expect(asked).To(ConsistOf("b-bridge", "a-prov"))
		})

		It("should keep an error entry for the bridge and ask the provisioner when no bridge answers", func() {
			agentList[1].Host = "127.0.0.1:1"
			agentList[2].Host = "127.0.0.1:1"

			resp, err := server.ListAgentStatus(userCtx(adminUser), &svclookup.ListAgentStatusRequest{InstanceUuid: "inst-1"})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetAgentStatus()).To(HaveLen(2))
			Expect(resp.GetAgentStatus()[0].GetAgentUuid()).To(Equal("b-bridge"))
			Expect(resp.GetAgentStatus()[1].GetAgentUuid()).To(Equal("a-prov"))
		})

		It("should never ask the provisioner for a status without agent uuid", func() {
			for range 20 {
				resp, err := server.GetStatus(userCtx(adminUser), &svclookup.GetStatusRequest{
					InstanceUuid: "inst-1", TypeUrl: "type", Identifier: "id",
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetError()).ToNot(Equal("a-prov"))
			}
		})

		It("should ask the provisioner for a status with its agent uuid", func() {
			resp, err := server.GetStatus(userCtx(adminUser), &svclookup.GetStatusRequest{
				InstanceUuid: "inst-1", AgentUuid: "a-prov", TypeUrl: "type", Identifier: "id",
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetError()).To(Equal("a-prov"))
		})

		It("should list the provisioners after the other agents", func() {
			listed, err := server.ListAgent(userCtx(adminUser), &svclookup.ListAgentRequest{InstanceUuid: "inst-1"})
			Expect(err).ToNot(HaveOccurred())
			Expect(listed.GetAgents()[0].GetUUID()).To(Equal("b-bridge"))
			Expect(listed.GetAgents()[2].GetUUID()).To(Equal("a-prov"))

			status, err := server.ListAgentStatus(userCtx(adminUser), &svclookup.ListAgentStatusRequest{
				InstanceUuid: "inst-1", AgentUuid: "",
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(status.GetAgentStatus()[0].GetAgentUuid()).To(Equal("b-bridge"))
			Expect(status.GetAgentStatus()[1].GetAgentUuid()).To(Equal("a-prov"))
		})
	})

	Context("provisioners on a sidecar instance", func() {
		var server *testServer
		BeforeEach(func() {
			serverTls, clientTls := rpctest.NewTLS("controller")
			features := &agent.AgentFeatures{ShowLogs: true, ShowStatus: true}
			agents := make([]*agent.Agent, 0, 3)
			for _, a := range []struct {
				name string
				mode agent.Agent_Mode
			}{{"a-prov", agent.Agent_PROVISIONER}, {"b-side", agent.Agent_SIDECAR}, {"c-side", agent.Agent_SIDECAR}} {
				agentServer := connect.NewServer()
				agentlogsconnect.RegisterLogsHandler(agentServer, labelledAgent{label: a.name})
				agentstatusconnect.RegisterResourceStatusHandler(agentServer, labelledAgent{label: a.name})
				addr, stop := rpctest.Serve(agentServer, serverTls)
				DeferCleanup(stop)
				agents = append(agents, &agent.Agent{UUID: a.name, Name: a.name, Mode: a.mode, Host: addr, Features: features})
			}
			inst := newFakeInstantiater()
			inst.agents = agents
			perm := permissioners.NewMetrics(permissioners.NewDbStorePerm(dbStoreMan))
			admConf := config.AdminConfig{Users: []string{adminUser}}
			flock := NewFlockServer(context.Background(), clientTls, admConf, nil, dbStoreMan, nil, nil, perm, inst, nil, lockers.NewMemory(), nil)
			server = &testServer{FlockServer: flock, perm: perm}
		})

		It("should ask every agent and the provisioner for the latest logs", func() {
			resp, err := server.GetLogsLatest(userCtx(adminUser), &svclookup.GetLogsRequest{InstanceUuid: "inst-1"})

			Expect(err).ToNot(HaveOccurred())
			asked := make([]string, 0, len(resp.GetLogs()))
			for _, logs := range resp.GetLogs() {
				asked = append(asked, logs.GetAgentUuid())
			}
			Expect(asked).To(ConsistOf("a-prov", "b-side", "c-side"))
		})

		It("should keep the logs of the others when an agent fails", func() {
			inst := newFakeInstantiater()
			inst.agents = []*agent.Agent{
				{UUID: "a-prov", Name: "a-prov", Mode: agent.Agent_PROVISIONER, Host: "127.0.0.1:1", Features: &agent.AgentFeatures{ShowLogs: true}},
				{UUID: "b-side", Name: "b-side", Mode: agent.Agent_SIDECAR, Host: "127.0.0.1:1", Features: &agent.AgentFeatures{ShowLogs: true}},
			}
			failing := NewFlockServer(context.Background(), nil, config.AdminConfig{Users: []string{adminUser}}, nil, dbStoreMan, nil, nil,
				permissioners.NewMetrics(permissioners.NewDbStorePerm(dbStoreMan)), inst, nil, lockers.NewMemory(), nil)

			resp, err := failing.GetLogsLatest(userCtx(adminUser), &svclookup.GetLogsRequest{InstanceUuid: "inst-1"})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetLogs()).To(HaveLen(2))
		})

		It("should skip the provisioner for the status calls without agent uuid", func() {
			for range 20 {
				status, err := server.GetStatus(userCtx(adminUser), &svclookup.GetStatusRequest{
					InstanceUuid: "inst-1", TypeUrl: "type", Identifier: "id",
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(status.GetError()).ToNot(Equal("a-prov"))

				list, err := server.ListStatus(userCtx(adminUser), &svclookup.ListStatusRequest{InstanceUuid: "inst-1", TypeUrl: "type"})
				Expect(err).ToNot(HaveOccurred())
				Expect(list.GetStatus()).To(HaveLen(1))

				types, err := server.ListResourceTypesStatus(userCtx(adminUser), &svclookup.ListResourceTypesRequest{InstanceUuid: "inst-1"})
				Expect(err).ToNot(HaveOccurred())
				Expect(types.GetResourceTypes()).To(HaveLen(1))
				Expect(types.GetResourceTypes()[0]).ToNot(Equal("a-prov-type"))
			}
		})
	})

	Context("ListResourceTypesStatus with an agent uuid", func() {
		It("should ask the provisioner it names", func() {
			serverTls, clientTls := rpctest.NewTLS("controller")
			agentServer := connect.NewServer()
			agentstatusconnect.RegisterResourceStatusHandler(agentServer, labelledAgent{label: "a-prov"})
			addr, stop := rpctest.Serve(agentServer, serverTls)
			DeferCleanup(stop)
			inst := newFakeInstantiater()
			inst.agents = []*agent.Agent{{UUID: "a-prov", Name: "a-prov", Mode: agent.Agent_PROVISIONER, Host: addr,
				Features: &agent.AgentFeatures{ShowStatus: true}}}
			perm := permissioners.NewMetrics(permissioners.NewDbStorePerm(dbStoreMan))
			flock := NewFlockServer(context.Background(), clientTls, config.AdminConfig{Users: []string{adminUser}}, nil,
				dbStoreMan, nil, nil, perm, inst, nil, lockers.NewMemory(), nil)

			types, err := flock.ListResourceTypesStatus(userCtx(adminUser),
				&svclookup.ListResourceTypesRequest{InstanceUuid: "inst-1", AgentUuid: "a-prov"})

			Expect(err).ToNot(HaveOccurred())
			Expect(types.GetResourceTypes()).To(ConsistOf("a-prov-type"))
		})
	})

	Context("ReadAgent", func() {
		var server *testServer
		BeforeEach(func() {
			inst := newFakeInstantiater()
			inst.agents = []*agent.Agent{{UUID: "a-prov", Name: "a-prov", Mode: agent.Agent_PROVISIONER}}
			server = newTestServer(inst)
			Expect(dbStoreMan.Instances().Create(context.Background(), &instance.Instance{UUID: "inst-1", Name: "inst-1"})).To(Succeed())
			node := &svclookup.InstanceNode{Node: &core.Node{}, AgentUuid: "a-prov", Name: "a-prov"}
			Expect(dbStoreMan.Nodes().ClaimNode(context.Background(), "inst-1", node, "10.0.0.1", 90*time.Second)).To(Succeed())
		})
		It("should answer the agent and its instance", func() {
			resp, err := server.ReadAgent(userCtx(adminUser), &svclookup.ReadAgentRequest{AgentUuid: "a-prov"})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetAgent().GetUUID()).To(Equal("a-prov"))
			Expect(resp.GetInstanceUuid()).To(Equal("inst-1"))
		})
		It("should answer NotFound for an unknown agent", func() {
			_, err := server.ReadAgent(userCtx(adminUser), &svclookup.ReadAgentRequest{AgentUuid: "unknown"})

			expectCode(err, connect.CodeNotFound)
		})
		It("should answer NotFound when the instance no longer declares the agent", func() {
			node := &svclookup.InstanceNode{Node: &core.Node{}, AgentUuid: "gone", Name: "gone"}
			Expect(dbStoreMan.Nodes().ClaimNode(context.Background(), "inst-1", node, "10.0.0.2", 90*time.Second)).To(Succeed())

			_, err := server.ReadAgent(userCtx(adminUser), &svclookup.ReadAgentRequest{AgentUuid: "gone"})

			expectCode(err, connect.CodeNotFound)
		})
		It("should be refused without the reader role on the instance", func() {
			_, err := server.ReadAgent(userCtx("bob"), &svclookup.ReadAgentRequest{AgentUuid: "a-prov"})

			expectCode(err, connect.CodePermissionDenied)
		})
	})
})
