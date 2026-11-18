//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"

	"github.com/seagalia/flock/e2e/controller/env"
	"github.com/seagalia/flock/e2e/controller/fakeagent"
)

const (
	lookupZone    = "zone-lookup"
	noFeatureZone = "zone-nofeature"
)

// controller dials the agents itself for lookups, so the fake agent listens on the host and controller reaches it through
// the testcontainers host access
var _ = Describe("Lookup", Ordered, func() {
	var (
		g                 *env.Controller
		fakeAgent         *fakeagent.Agent
		alice, bob, root  *env.Api
		instanceUuid      string
		aliceVipUuid      string
		noFeatureInstUuid string
	)

	BeforeAll(func(ctx SpecContext) {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		fakeAgent = fakeagent.NewAgent(e2eEnv.Certs.AgentTLSConfig())
		serveDone := make(chan error, 1)
		go func() { serveDone <- fakeAgent.Serve(lis) }()
		DeferCleanup(func() {
			fakeAgent.Stop()
			Expect(<-serveDone).To(Succeed())
		})
		agentPort := lis.Addr().(*net.TCPAddr).Port

		dsn, err := e2eEnv.StartPostgres(ctx)
		Expect(err).NotTo(HaveOccurred())
		provider, err := e2eEnv.StartOauth2(ctx)
		Expect(err).NotTo(HaveOccurred())
		g = startController(ctx, env.ControllerOptions{
			Name:          "controller-lookup",
			Store:         env.StorePostgres,
			DSN:           dsn,
			Oidc:          provider,
			HostPorts:     []int{agentPort},
			Instantiators: lookupInstantiators(agentPort),
		})
		alice = loginOidc(ctx, g, provider, "alice")
		bob = loginOidc(ctx, g, provider, "bob")
		root = loginOidc(ctx, g, provider, "root")

		zoneUuids := make(map[string]string)
		for _, zoneName := range []string{lookupZone, noFeatureZone} {
			zoneResp, err := root.Zone.CreateZone(ctx, &svczone.CreateZoneRequest{Zone: env.NewZone(zoneName)})
			Expect(err).NotTo(HaveOccurred())
			zoneUuids[zoneName] = zoneResp.GetZone().GetUUID()
		}
		// only an admin creates vips, alice's one is granted to her
		aliceVipUuid = createVip(ctx, root)
		grant(ctx, root, vipType, aliceVipUuid, permission.Role_OWNER, user("alice"))
		Eventually(func() error {
			_, err := alice.VIP.ReadVIP(ctx, &svccore.UuidRequest{UUID: aliceVipUuid})
			return err
		}).Should(Succeed())
		instanceUuid = createAliceInstance(ctx, alice, zoneUuids[lookupZone], aliceVipUuid)
		noFeatureInstUuid = createAliceInstance(ctx, alice, zoneUuids[noFeatureZone])
	}, NodeTimeout(10*time.Minute))

	BeforeEach(func() {
		registerControllerForDump(g)
	})

	It("lists the agents", func(ctx SpecContext) {
		resp, err := alice.Lookup.ListAgent(ctx, &svclookup.ListAgentRequest{InstanceUuid: instanceUuid})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.GetAgents()).To(ConsistOf(
			HaveField("UUID", "agent-1"),
			HaveField("UUID", "agent-down"),
		))
	}, NodeTimeout(time.Minute))

	It("relays latest and streamed logs", func(ctx SpecContext) {
		fakeAgent.SetLatestLogs("l1", "l2")

		latest, err := alice.Lookup.GetLogsLatest(ctx, &svclookup.GetLogsRequest{InstanceUuid: instanceUuid})
		Expect(err).NotTo(HaveOccurred())
		Expect(latest.GetLogs()).To(HaveLen(1))
		Expect(latest.GetLogs()[0].GetAgentUuid()).To(Equal("agent-1"))
		Expect(latest.GetLogs()[0].GetLogLines()).To(Equal([]string{"l1", "l2"}))

		streamCtx, cancelStream := context.WithCancel(ctx)
		DeferCleanup(cancelStream)
		stream, err := alice.Lookup.GetLogsStream(streamCtx, &svclookup.GetLogsRequest{InstanceUuid: instanceUuid})
		Expect(err).NotTo(HaveOccurred())
		received := make(chan *svclookup.LogsStreamResponse_AgentLogLine, 10)
		go func() {
			for {
				resp, err := stream.Receive()
				if err != nil {
					return
				}
				if line := resp.GetLogStream(); line != nil {
					received <- line
				}
			}
		}()
		// a line emitted before controller follows the agent would go nowhere
		Eventually(fakeAgent.Followers).Should(Equal(1))

		fakeAgent.EmitLog("live")

		Eventually(received).Should(Receive(And(
			HaveField("AgentUuid", "agent-1"),
			HaveField("LogLine", "live"),
		)))
	}, NodeTimeout(time.Minute))

	It("relays resource statuses", func(ctx SpecContext) {
		fakeAgent.SetStatus(&agentstatus.StatusResponse{State: agentstatus.StatusResponse_ERRORED, Error: "vip broken"})
		fakeAgent.SetListStatus(&agentstatus.ListStatusResponse{Statuses: []*agentstatus.StatusResponse{
			{State: agentstatus.StatusResponse_OK},
			{State: agentstatus.StatusResponse_ERRORED, Error: "other vip broken"},
		}})
		fakeAgent.SetResourceTypes(vipType, vsType)

		// controller tries the agents in map order and keeps the first it can reach, so agent-down comes first in about
		// half of the calls, regression: an agent that could not be dialed was kept
		for range 10 {
			st, err := alice.Lookup.GetStatus(ctx, &svclookup.GetStatusRequest{
				InstanceUuid: instanceUuid, TypeUrl: vipType, Identifier: aliceVipUuid,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(st.GetState()).To(Equal(svclookup.StatusResponse_ERRORED))
			Expect(st.GetError()).To(Equal("vip broken"))

			list, err := alice.Lookup.ListStatus(ctx, &svclookup.ListStatusRequest{InstanceUuid: instanceUuid, TypeUrl: vipType})
			Expect(err).NotTo(HaveOccurred())
			Expect(list.GetStatus()).To(ConsistOf(
				And(HaveField("State", svclookup.StatusResponse_OK), HaveField("Error", "")),
				And(HaveField("State", svclookup.StatusResponse_ERRORED), HaveField("Error", "other vip broken")),
			))

			types, err := alice.Lookup.ListResourceTypesStatus(ctx, &svclookup.ListResourceTypesRequest{InstanceUuid: instanceUuid})
			Expect(err).NotTo(HaveOccurred())
			Expect(types.GetResourceTypes()).To(ConsistOf(vipType, vsType))
		}
	}, NodeTimeout(time.Minute))

	// agent-down fails at GetAgentStatus, so this proves the failure is reported once per agent next to a live one,
	// the failed dial itself is covered by the "[::1" unit spec
	It("reports agent statuses with one error entry for an unreachable agent", func(ctx SpecContext) {
		fakeAgent.SetAgentStatus(&agent.AgentStatus{State: &agent.AgentStatus_SidecarAgentState{
			SidecarAgentState: &agent.SidecarAgentState{AgentState: &agent.State{Name: "agent-1", Status: agent.Status_OK}},
		}})

		resp, err := alice.Lookup.ListAgentStatus(ctx, &svclookup.ListAgentStatusRequest{InstanceUuid: instanceUuid})
		Expect(err).NotTo(HaveOccurred())
		statuses := map[string]*agent.State{}
		for _, st := range resp.GetAgentStatus() {
			Expect(statuses).NotTo(HaveKey(st.GetAgentUuid()), "agent %s reported twice", st.GetAgentUuid())
			statuses[st.GetAgentUuid()] = st.GetAgentStatus().GetSidecarAgentState().GetAgentState()
		}
		Expect(statuses).To(HaveLen(2))
		Expect(statuses).To(HaveKeyWithValue("agent-1", HaveField("Status", agent.Status_OK)))
		Expect(statuses).To(HaveKeyWithValue("agent-down", And(
			HaveField("Status", agent.Status_ERRORED),
			HaveField("Details", Not(BeEmpty())),
		)))
	}, NodeTimeout(time.Minute))

	It("hides announcing vips a user cannot read", func(ctx SpecContext) {
		adminVipUuid := createVip(ctx, root)
		fakeAgent.SetAgentStatus(&agent.AgentStatus{State: &agent.AgentStatus_SidecarAgentState{
			SidecarAgentState: &agent.SidecarAgentState{
				AgentState:     &agent.State{Name: "agent-1", Status: agent.Status_OK},
				AnnouncingVips: []*vip.VIP{{UUID: aliceVipUuid}, {UUID: adminVipUuid}},
			},
		}})

		Expect(announcedVips(ctx, alice, instanceUuid)).To(ConsistOf(aliceVipUuid))
		Expect(announcedVips(ctx, root, instanceUuid)).To(ConsistOf(aliceVipUuid, adminVipUuid))
	}, NodeTimeout(time.Minute))

	It("skips agents without the feature", func(ctx SpecContext) {
		_, err := alice.Lookup.GetLogsLatest(ctx, &svclookup.GetLogsRequest{InstanceUuid: noFeatureInstUuid})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnimplemented), "%v", err)

		_, err = alice.Lookup.GetStatus(ctx, &svclookup.GetStatusRequest{
			InstanceUuid: noFeatureInstUuid, TypeUrl: vipType, Identifier: aliceVipUuid,
		})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnimplemented), "%v", err)
	}, NodeTimeout(time.Minute))

	It("refuses lookups on an instance the user cannot read", func(ctx SpecContext) {
		lookups := map[string]func() error{
			"ListAgent": func() error {
				_, err := bob.Lookup.ListAgent(ctx, &svclookup.ListAgentRequest{InstanceUuid: instanceUuid})
				return err
			},
			"GetLogsLatest": func() error {
				_, err := bob.Lookup.GetLogsLatest(ctx, &svclookup.GetLogsRequest{InstanceUuid: instanceUuid})
				return err
			},
			"GetLogsStream": func() error {
				stream, err := bob.Lookup.GetLogsStream(ctx, &svclookup.GetLogsRequest{InstanceUuid: instanceUuid})
				if err != nil {
					return err
				}
				_, err = stream.Receive()
				return err
			},
			"GetStatus": func() error {
				_, err := bob.Lookup.GetStatus(ctx, &svclookup.GetStatusRequest{
					InstanceUuid: instanceUuid, TypeUrl: vipType, Identifier: aliceVipUuid,
				})
				return err
			},
			"ListStatus": func() error {
				_, err := bob.Lookup.ListStatus(ctx, &svclookup.ListStatusRequest{InstanceUuid: instanceUuid, TypeUrl: vipType})
				return err
			},
			"ListResourceTypesStatus": func() error {
				_, err := bob.Lookup.ListResourceTypesStatus(ctx, &svclookup.ListResourceTypesRequest{InstanceUuid: instanceUuid})
				return err
			},
			"ListAgentStatus": func() error {
				_, err := bob.Lookup.ListAgentStatus(ctx, &svclookup.ListAgentStatusRequest{InstanceUuid: instanceUuid})
				return err
			},
		}
		for name, lookup := range lookups {
			err := lookup()
			Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied), "%s: %v", name, err)
		}
	}, NodeTimeout(time.Minute))

	// getHttp gives the body of a plain https GET on controller, its http routes need no token
	getHttp := func(ctx SpecContext, path string) string {
		GinkgoHelper()
		client := &http.Client{
			Transport: &http.Transport{TLSClientConfig: e2eEnv.Certs.UserTLSConfig(env.HostServerName)},
			Timeout:   30 * time.Second,
		}
		resp, err := client.Get(fmt.Sprintf("https://%s%s", g.Addr(ctx), path))
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK), "%s", body)
		return string(body)
	}

	It("proxies agent metrics with agent labels", func(ctx SpecContext) {
		fakeAgent.SetMetrics("requests_total{code=\"200\"} 1\n")

		body := getHttp(ctx, "/metrics/"+instanceUuid)

		Expect(body).To(MatchRegexp(`requests_total\{[^}]*agent_uuid="agent-1"[^}]*\} 1`))
	}, NodeTimeout(time.Minute))

	It("serves its own metrics with the calls it served", func(ctx SpecContext) {
		body := getHttp(ctx, "/metrics")

		createZone := `grpc_method="CreateZone",grpc_service="flock.services.zone.v1.Zone",grpc_type="unary"`
		Expect(body).To(MatchRegexp(`grpc_server_handled_total\{grpc_code="OK",` + createZone + `\} [1-9]`))
		Expect(body).To(MatchRegexp(`grpc_server_started_total\{` + createZone + `\} [1-9]`))
		Expect(body).To(ContainSubstring(`grpc_server_handled_total{grpc_code="DataLoss",`+createZone+`} 0`),
			"every code of every method is exposed before its first call")
		Expect(body).To(MatchRegexp(`grpc_server_started_total\{grpc_method="DeltaAggregatedResources",` +
			`grpc_service="flock.services.discovery.v1.AggregatedDiscovery",grpc_type="bidi_stream"\} \d+`))
	}, NodeTimeout(time.Minute))

	It("lists the instances with their metrics endpoint", func(ctx SpecContext) {
		body := getHttp(ctx, "/metrics/instances-info")

		var infos []map[string]string
		Expect(json.Unmarshal([]byte(body), &infos)).To(Succeed())
		Expect(infos).To(ContainElement(And(
			HaveKeyWithValue("uuid", instanceUuid),
			HaveKeyWithValue("metric_endpoint", HaveSuffix("/metrics/"+instanceUuid)),
		)))
	}, NodeTimeout(time.Minute))
})

// lookupInstantiators puts the reachable agents at the fake agent, agent-down at a port nothing listens on
func lookupInstantiators(agentPort int) []map[string]any {
	agentHost := fmt.Sprintf("host.testcontainers.internal:%d", agentPort)
	smallType := []map[string]any{{"name": "small"}}
	return []map[string]any{
		{
			"name":           "static",
			"zones":          []string{lookupZone},
			"resource_types": smallType,
			"static": map[string]any{"agents": []map[string]any{
				{
					"uuid": "agent-1", "name": "agent-1", "host": agentHost,
					"features": map[string]any{"show_logs": true, "show_status": true, "show_metrics": true},
				},
				{
					"uuid": "agent-down", "name": "agent-down", "host": "host.testcontainers.internal:1",
					"features": map[string]any{"show_status": true},
				},
			}},
		},
		{
			"name":           "static-nofeature",
			"zones":          []string{noFeatureZone},
			"resource_types": smallType,
			"static": map[string]any{"agents": []map[string]any{
				{"uuid": "agent-plain", "name": "agent-plain", "host": agentHost},
			}},
		},
	}
}

func createAliceInstance(ctx context.Context, alice *env.Api, zoneUuid string, vipUuids ...string) string {
	GinkgoHelper()
	resp, err := alice.Instance.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{
		Instance: env.NewInstance(env.Unique("inst"), zoneUuid, vipUuids...),
	})
	Expect(err).NotTo(HaveOccurred())
	return resp.GetInstance().GetUUID()
}

func announcedVips(ctx context.Context, api *env.Api, instanceUuid string) []string {
	GinkgoHelper()
	resp, err := api.Lookup.ListAgentStatus(ctx, &svclookup.ListAgentStatusRequest{
		InstanceUuid: instanceUuid, AgentUuid: "agent-1", ShowAnnouncingVips: true,
	})
	Expect(err).NotTo(HaveOccurred())
	Expect(resp.GetAgentStatus()).To(HaveLen(1))
	vipUuids := []string{}
	for _, announced := range resp.GetAgentStatus()[0].GetAgentStatus().GetSidecarAgentState().GetAnnouncingVips() {
		vipUuids = append(vipUuids, announced.GetUUID())
	}
	return vipUuids
}
