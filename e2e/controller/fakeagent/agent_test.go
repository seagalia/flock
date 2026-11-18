//go:build e2e

package fakeagent_test

import (
	"crypto/tls"

	"connectrpc.com/connect/v2"
	"io"
	"net"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/e2e/controller/fakeagent"
	agent "github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1/agentstatusconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("Agent", func() {
	var fakeAgent *fakeagent.Agent
	var addr string
	var conn *connect.Client

	dial := func(tlsConf *tls.Config) *connect.Client {
		clients := helpers.NewGrpcClients(tlsConf)
		DeferCleanup(clients.Close)
		return clients.Client(addr)
	}

	BeforeEach(func() {
		fakeAgent = fakeagent.NewAgent(certs.AgentTLSConfig())
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		addr = lis.Addr().String()
		go func() {
			defer GinkgoRecover()
			Expect(fakeAgent.Serve(lis)).To(Succeed())
		}()
		DeferCleanup(fakeAgent.Stop)
		conn = dial(certs.ControllerAgentTLSConfig())
	})

	It("serves the scripted latest logs", func(ctx SpecContext) {
		fakeAgent.SetLatestLogs("line 1", "line 2")

		latest, err := agentlogsconnect.NewLogsClient(conn).Latest(ctx, &emptypb.Empty{})

		Expect(err).NotTo(HaveOccurred())
		Expect(latest.GetLogLines()).To(Equal([]string{"line 1", "line 2"}))
	})

	It("sends emitted logs to followers", func(ctx SpecContext) {
		follow, err := agentlogsconnect.NewLogsClient(conn).Follow(ctx, &emptypb.Empty{})
		Expect(err).NotTo(HaveOccurred())
		Eventually(fakeAgent.Followers).Should(Equal(1))

		started, err := follow.Receive()
		Expect(err).NotTo(HaveOccurred())
		Expect(started.GetStarted()).NotTo(BeNil())

		fakeAgent.EmitLog("live line")

		received, err := follow.Receive()
		Expect(err).NotTo(HaveOccurred())
		Expect(received.GetLogLine()).To(Equal("live line"))
	})

	It("serves the scripted statuses", func(ctx SpecContext) {
		resourceStatus := &agentstatus.StatusResponse{State: agentstatus.StatusResponse_State(1), Error: "boom"}
		fakeAgent.SetStatus(resourceStatus)
		fakeAgent.SetListStatus(&agentstatus.ListStatusResponse{Statuses: []*agentstatus.StatusResponse{resourceStatus}})
		fakeAgent.SetResourceTypes("type-a", "type-b")
		agentStatus := &agent.AgentStatus{State: &agent.AgentStatus_ProxyAgentState{}}
		fakeAgent.SetAgentStatus(agentStatus)
		statusClient := agentstatusconnect.NewResourceStatusClient(conn)

		gotStatus, err := statusClient.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: "type-a", Identifier: "x"})
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(gotStatus, resourceStatus)).To(BeTrue())

		gotList, err := statusClient.ListStatus(ctx, &agentstatus.ListStatusRequest{TypeUrl: "type-a"})
		Expect(err).NotTo(HaveOccurred())
		Expect(gotList.GetStatuses()).To(HaveLen(1))

		gotTypes, err := statusClient.ListResourceTypes(ctx, &emptypb.Empty{})
		Expect(err).NotTo(HaveOccurred())
		Expect(gotTypes.GetResourceTypes()).To(Equal([]string{"type-a", "type-b"}))

		gotAgentStatus, err := agentstatusconnect.NewAgentStatusClient(conn).GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(gotAgentStatus, agentStatus)).To(BeTrue())
	})

	It("serves the scripted metrics over https", func() {
		fakeAgent.SetMetrics("flock_e2e_metric 42\n")
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: certs.ControllerAgentTLSConfig()}}

		resp, err := client.Get("https://" + addr + "/metrics")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(string(body)).To(Equal("flock_e2e_metric 42\n"))
	})

	It("refuses a client without certificate", func(ctx SpecContext) {
		noCertConf := certs.ControllerAgentTLSConfig()
		noCertConf.Certificates = nil
		noCertConn := dial(noCertConf)

		_, err := agentlogsconnect.NewLogsClient(noCertConn).Latest(ctx, &emptypb.Empty{})

		Expect(err).To(HaveOccurred())
	})
})
