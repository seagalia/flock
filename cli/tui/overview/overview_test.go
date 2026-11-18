package overview

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"connectrpc.com/connect/v2"
	"github.com/charmbracelet/x/ansi"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli/tui/tuicommon"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/seagalia/flock/cli/internal/fakeconnect"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
)

// pagedReply answers the first page of a list with resp and the next pages with empty, like the controller
func pagedReply(resp, empty proto.Message, offsetOf func(proto.Message) int32) fakeconnect.Handler {
	return func(req proto.Message) (proto.Message, error) {
		if offsetOf(req) > 0 {
			return empty, nil
		}
		return resp, nil
	}
}

func reply(resp proto.Message) fakeconnect.Handler {
	return func(proto.Message) (proto.Message, error) {
		return resp, nil
	}
}

func httpVs(uuid, name string, port uint32, backendUuid string) *vs.VirtualServer {
	return &vs.VirtualServer{UUID: uuid, Name: name, Port: port, RouteConfig: &vs.VirtualServer_HttpRoutesConfig{
		HttpRoutesConfig: &vs.HttpRoutesConfig{VirtualHosts: []*route.VirtualHost{{
			Domains: []string{"*"},
			Routes:  []*route.Route{{Action: &route.Route_BackendUuid{BackendUuid: backendUuid}}},
		}}},
	}}
}

func endpointStatus(backendUuid string, endpoints ...*backend.EndpointStat) *svclookup.StatusResponse {
	stats, err := anypb.New(&backend.BackendEndpointStats{BackendUuid: backendUuid, EndpointStats: endpoints})
	Expect(err).NotTo(HaveOccurred())
	return &svclookup.StatusResponse{PreviousResource: stats}
}

func endpoint(ip string, port uint32, healthy bool, lastError string) *backend.EndpointStat { //nolint:unparam // the call sites show the value
	stat := &backend.EndpointStat{IpAddr: &core.IPAddr{Ip: ip, Port: port}, Healthy: healthy}
	if lastError != "" {
		stat.LastError = wrapperspb.String(lastError)
	}
	return stat
}

// run gives the messages of cmd to the overview until no command is left,
// only for the data loading commands: a cursor blink would never end
func run(m *Overview, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, batchCmd := range batch {
			run(m, batchCmd)
		}
		return
	}
	run(m, m.Update(msg))
}

func press(m *Overview, keys string) tea.Cmd {
	var cmd tea.Cmd
	for _, r := range keys {
		cmd = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return cmd
}

func screen(m *Overview) string {
	return ansi.Strip(m.View())
}

var _ = Describe("Overview", func() {
	var (
		conn     *fakeconnect.Conn
		model    *Overview
		vssReply []*vs.VirtualServer
		statuses []*svclookup.StatusResponse
	)

	BeforeEach(func() {
		vssReply = []*vs.VirtualServer{
			httpVs("vs-web", "web", 80, "c-web"),
			{UUID: "vs-dns", Name: "dns", Port: 53, RouteConfig: &vs.VirtualServer_UdpRouteConfig{
				UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: "c-dns"},
			}},
		}
		statuses = []*svclookup.StatusResponse{
			endpointStatus("c-web",
				endpoint("10.0.0.1", 8080, true, ""),
				endpoint("10.0.0.2", 8080, false, "connection refused"),
			),
		}
		conn = fakeconnect.New().
			Handle(svcinstanceconnect.InstanceListInstanceProcedure, pagedReply(
				&svcinstance.ListInstanceResponse{Instances: []*instance.Instance{{UUID: "inst-1", Name: "local"}, {UUID: "inst-2", Name: "other"}}},
				&svcinstance.ListInstanceResponse{},
				func(req proto.Message) int32 { return req.(*svcinstance.ListInstanceRequest).GetOffset() },
			)).
			Handle(svcvipconnect.VIPListVIPProcedure, pagedReply(
				&svcvip.ListVIPResponse{Vip: []*vip.VIP{
					{UUID: "vip-1", Ip: "192.168.1.10", VirtualServersUuid: []string{"vs-web", "vs-dns"}},
					{UUID: "vip-2", Ip: "192.168.1.11", VirtualServersUuid: []string{"vs-web"}},
				}},
				&svcvip.ListVIPResponse{},
				func(req proto.Message) int32 { return req.(*svcvip.ListVIPRequest).GetOffset() },
			)).
			Handle(svcvsconnect.VirtualServerListVSProcedure, func(req proto.Message) (proto.Message, error) {
				if req.(*svcvs.ListVSRequest).GetOffset() > 0 {
					return &svcvs.ListVSResponse{}, nil
				}
				return &svcvs.ListVSResponse{Vs: vssReply}, nil
			}).
			Handle(svclookupconnect.LookupListAgentProcedure, reply(&svclookup.ListAgentResponse{Agents: []*agent.Agent{
				{UUID: "agent-1", Features: &agent.AgentFeatures{ShowStatus: true}},
				{UUID: "acme-1", Mode: agent.Agent_PROVISIONER, Features: &agent.AgentFeatures{ShowStatus: true}},
			}})).
			Handle(svclookupconnect.LookupListAgentStatusProcedure, reply(&svclookup.ListAgentStatusResponse{AgentStatus: []*svclookup.GetAgentStatusResponse{
				{AgentUuid: "agent-1", AgentStatus: &agent.AgentStatus{State: &agent.AgentStatus_SidecarAgentState{SidecarAgentState: &agent.SidecarAgentState{
					AgentState:      &agent.State{Name: "node-1", Status: agent.Status_OK},
					ComponentStates: []*agent.State{{Name: "envoy", Status: agent.Status_OK}, {Name: "ipvs", Status: agent.Status_ERRORED}},
				}}}},
				{AgentUuid: "acme-1", AgentStatus: &agent.AgentStatus{State: &agent.AgentStatus_SidecarAgentState{SidecarAgentState: &agent.SidecarAgentState{
					AgentState: &agent.State{Name: "acme-node", Status: agent.Status_ERRORED},
				}}}},
			}})).
			Handle(svclookupconnect.LookupListStatusProcedure, func(proto.Message) (proto.Message, error) {
				return &svclookup.ListStatusResponse{Status: statuses}, nil
			}).
			Handle(svcbackendconnect.BackendReadBackendProcedure, func(req proto.Message) (proto.Message, error) {
				uuid := req.(*svccore.UuidRequest).GetUUID()
				if uuid != "c-web" {
					return nil, connect.NewError(connect.CodeNotFound, "backend not found")
				}
				return &svcbackend.BackendResponse{Backend: &backend.Backend{
					UUID: "c-web", Name: "web-backends", LbPolicy: backend.Backend_ROUND_ROBIN,
					Endpoint: &backend.Backend_IpAddrGroup{IpAddrGroup: &core.IPAddrGroup{IpAddresses: []*core.IPAddr{
						{Ip: "10.0.0.1", Port: 8080, LoadBalancingWeight: 3},
						{Ip: "10.0.0.2", Port: 8080},
						{Ip: "10.0.0.3", Port: 8080},
					}}},
					HealthCheck: &core.HealthCheck{HealthChecker: &core.HealthCheck_HttpHealthCheck{HttpHealthCheck: &core.HttpHealthCheck{Path: "/"}}},
				}}, nil
			})
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		model = NewOverview(ctx, clients.New(conn.Client()))
		model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	})

	load := func() {
		GinkgoHelper()
		run(model, model.Init())
	}

	It("lists the virtual servers of the first instance with their addresses and endpoint health", func() {
		load()

		view := screen(model)
		Expect(view).To(ContainSubstring("instance local"))
		Expect(view).To(MatchRegexp(`▸ web\s+HTTP\s+192\.168\.1\.10:80\s+● 1/2 healthy`))
		Expect(view).To(MatchRegexp(`\n\s+192\.168\.1\.11:80\s*\n`))
		Expect(view).To(MatchRegexp(`▸ dns\s+UDP\s+192\.168\.1\.10:53\s+no endpoint reported`))
	})

	It("summarizes the agents on one line", func() {
		load()

		Expect(screen(model)).To(ContainSubstring("● 1/1 agents alive · node-1 Started 1/2 components"))
		Expect(screen(model)).NotTo(ContainSubstring("acme-node"))
	})

	It("expands the selected virtual server with its backends and endpoints", func() {
		load()

		run(model, press(model, " "))

		view := screen(model)
		Expect(view).To(ContainSubstring("▾ web"))
		Expect(view).To(ContainSubstring("backend web-backends  static · Round Robin · hc HTTP · proxy protocol off"))
		Expect(view).To(MatchRegexp(`● 10\.0\.0\.1:8080\s+healthy\s+weight 3`))
		Expect(view).To(MatchRegexp(`● 10\.0\.0\.2:8080\s+unhealthy\s+connection refused`))
		Expect(view).To(MatchRegexp(`○ 10\.0\.0\.3:8080\s+no status`))
	})

	It("shows a backend which could not be read under its virtual server", func() {
		load()

		press(model, "j")
		run(model, press(model, " "))

		Expect(screen(model)).To(ContainSubstring("backend c-dns backend not found (Code: NotFound)"))
	})

	It("collapses an expanded virtual server", func() {
		load()
		run(model, press(model, " "))

		press(model, " ")

		Expect(screen(model)).NotTo(ContainSubstring("web-backends"))
	})

	It("filters the virtual servers by name while typing, q included", func() {
		load()

		press(model, "s")
		press(model, "dnq")
		Expect(model.CapturesInput()).To(BeTrue())
		Expect(screen(model)).To(ContainSubstring("No virtual server matches the filter."))

		model.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
		model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		Expect(model.CapturesInput()).To(BeFalse())
		view := screen(model)
		Expect(view).To(ContainSubstring("dns"))
		Expect(view).NotTo(ContainSubstring("web"))

		model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		Expect(screen(model)).To(ContainSubstring("web"))
	})

	It("keeps the selected virtual server on screen when the list is taller than the terminal", func() {
		vssReply = make([]*vs.VirtualServer, 0, 30)
		for i := range 30 {
			vssReply = append(vssReply, httpVs("vs-"+string(rune('a'+i)), "vs-"+string(rune('a'+i)), 80, "c-web"))
		}
		model.Update(tea.WindowSizeMsg{Width: 120, Height: 15})
		load()

		for range 25 {
			press(model, "j")
		}

		view := screen(model)
		Expect(strings.Count(view, "\n")).To(BeNumerically("<", 15))
		Expect(view).To(ContainSubstring("▸ vs-z"))
		Expect(view).NotTo(ContainSubstring("▸ vs-a "))
	})

	It("reloads the endpoint health on r", func() {
		load()
		statuses = []*svclookup.StatusResponse{
			endpointStatus("c-web", endpoint("10.0.0.1", 8080, true, ""), endpoint("10.0.0.2", 8080, true, "")),
		}

		run(model, press(model, "r"))

		Expect(screen(model)).To(ContainSubstring("● 2/2 healthy"))
	})

	It("cancels the load in flight when it reloads", func() {
		load()
		previousLoad := model.loadCtx

		run(model, press(model, "r"))

		Expect(previousLoad.Err()).To(MatchError(context.Canceled))
		Expect(model.loadCtx.Err()).NotTo(HaveOccurred())
	})

	It("drops the answer of a load that a reload replaced", func() {
		load()
		fresh := []*svclookup.StatusResponse{
			endpointStatus("c-web", endpoint("10.0.0.1", 8080, true, ""), endpoint("10.0.0.2", 8080, true, "")),
		}
		var statusCalls atomic.Int32
		slow := make(chan struct{})
		var release sync.Once
		answerSlowLoad := func() { release.Do(func() { close(slow) }) }
		DeferCleanup(answerSlowLoad)
		conn.Handle(svclookupconnect.LookupListStatusProcedure, func(proto.Message) (proto.Message, error) {
			if statusCalls.Add(1) == 1 {
				// the first load is slow and still waiting when the reload replaces it
				<-slow
				return &svclookup.ListStatusResponse{Status: statuses}, nil
			}
			return &svclookup.ListStatusResponse{Status: fresh}, nil
		})
		model.Update(tuicommon.ReloadMsg(true))
		staleLoad := loadInstance(model.loadCtx, model.client, model.inst)
		stale := make(chan tea.Msg, 1)
		go func() { stale <- staleLoad() }()
		Eventually(statusCalls.Load).Should(Equal(int32(1)))

		run(model, press(model, "r"))

		answerSlowLoad()
		Eventually(stale).Should(Receive(BeNil()))
		Expect(screen(model)).To(ContainSubstring("● 2/2 healthy"))
	})

	It("keeps the last data and shows the error when a reload fails", func() {
		load()
		conn.Handle(svclookupconnect.LookupListStatusProcedure, func(proto.Message) (proto.Message, error) {
			return nil, connect.NewError(connect.CodeUnavailable, "controller unreachable")
		})

		run(model, press(model, "r"))

		view := screen(model)
		Expect(view).To(ContainSubstring("could not load the instance: controller unreachable (Code: Unavailable)"))
		Expect(view).To(ContainSubstring("▸ web"))
	})

	It("tells when the controller has no instance", func() {
		conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, reply(&svcinstance.ListInstanceResponse{}))

		load()

		Expect(screen(model)).To(ContainSubstring("No instance found"))
	})
})
