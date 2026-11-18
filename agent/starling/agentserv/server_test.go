package agentserv_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/seagalia/flock/sdk/agentsdk/loghooks"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	agentlogs "github.com/seagalia/flock/sdk/flock/services/agent/logs/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	agentmgmt "github.com/seagalia/flock/sdk/flock/services/agent/manage/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1/agentstatusconnect"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/rpcserver"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/seagalia/flock/agent/starling/agentlist"
	. "github.com/seagalia/flock/agent/starling/agentserv"
)

type fakeEndpointStats struct {
	stats map[string]*backend.BackendEndpointStats
}

func (f *fakeEndpointStats) EndpointStatsByBackends() []*backend.BackendEndpointStats {
	result := make([]*backend.BackendEndpointStats, 0, len(f.stats))
	for _, stat := range f.stats {
		result = append(result, stat)
	}
	return result
}

func (f *fakeEndpointStats) EndpointStatsForBackend(backendUuid string) *backend.BackendEndpointStats {
	return f.stats[backendUuid]
}

// fakeFollowServer is the connect stream of a Follow call
type fakeFollowServer struct {
	mutex   sync.Mutex
	lines   []string
	sending int
	errSend error
	// startedFirst tells the started message came before any line, it is kept apart from the lines
	startedFirst bool
}

func (f *fakeFollowServer) Receive(any) error {
	return nil
}

func (f *fakeFollowServer) SendHeaders() error {
	return nil
}

func (f *fakeFollowServer) Send(msg any) error {
	resp := msg.(*agentlogs.LogStreamResponse)
	f.mutex.Lock()
	f.sending++
	concurrent := f.sending > 1
	f.mutex.Unlock()
	defer func() {
		f.mutex.Lock()
		f.sending--
		f.mutex.Unlock()
	}()
	if concurrent {
		return fmt.Errorf("concurrent send on stream")
	}
	time.Sleep(time.Millisecond)
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if resp.GetStarted() != nil {
		f.startedFirst = len(f.lines) == 0
	} else {
		f.lines = append(f.lines, resp.GetLogLine())
	}
	return f.errSend
}

func (f *fakeFollowServer) Lines() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return append([]string{}, f.lines...)
}

func announcedVip(uuid, ip string, typeAnnounce starling_announce.StarlingAnnounce_TypeAnnounce) *vip.VIP {
	announce, err := anypb.New(&starling_announce.StarlingAnnounce{TypeAnnounce: typeAnnounce})
	Expect(err).ToNot(HaveOccurred())
	return &vip.VIP{UUID: uuid, Ip: ip, TypedAnnounce: announce}
}

var _ = Describe("AgentServer", func() {
	var (
		edpStats   *fakeEndpointStats
		logHook    *loghooks.ListenerWriter
		tlsInfo    TlsInfo
		envoyAlive error
		wafAlive   error
		placer     *agentlist.VipPlacer
		server     *AgentServer
		ctx        = context.Background()
	)

	newServer := func() *AgentServer {
		return NewAgentServer("node-1", edpStats, logHook, tlsInfo, func() error { return envoyAlive }, func() error { return wafAlive }, placer, rt.Store.VIPs(), rt.PrevStore, rt.States, rt.SecretLinks)
	}

	BeforeEach(func() {
		edpStats = &fakeEndpointStats{stats: map[string]*backend.BackendEndpointStats{}}
		logHook = loghooks.NewListenerWriter()
		tmpDir := GinkgoT().TempDir()
		tlsInfo = TlsInfo{
			CAFilePath:   filepath.Join(tmpDir, "ca.pem"),
			CertFilePath: filepath.Join(tmpDir, "cert.pem"),
			KeyFilePath:  filepath.Join(tmpDir, "key.pem"),
		}
		envoyAlive = nil
		wafAlive = nil
		placer = nil
		server = newServer()
	})

	Context("resource status", func() {
		It("should list resource types", func() {
			resp, err := server.ListResourceTypes(ctx, &emptypb.Empty{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetResourceTypes()).To(ContainElements(
				helpers.TypeUrl[*backend.Backend](),
				helpers.TypeUrl[*vip.VIP](),
				helpers.TypeUrl[*backend.BackendEndpointStats](),
			))
		})

		It("should reject invalid requests", func() {
			// validation runs in the connect chain of the agent, as for a real call
			connectServer := rpcserver.NewServer(func(ctx context.Context, _ string) (context.Context, error) {
				return ctx, nil
			})
			agentstatusconnect.RegisterResourceStatusHandler(connectServer, server)
			client := agentstatusconnect.NewResourceStatusClient(connect.NewClient(connectinprocess.New(connectServer)))

			_, err := client.ListStatus(ctx, &agentstatus.ListStatusRequest{})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
			_, err = client.GetStatus(ctx, &agentstatus.GetStatusRequest{})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
		})

		It("should give status ok or errored for applied resources", func() {
			okBackend := &backend.Backend{UUID: "backend-ok", Name: "ok"}
			errBackend := &backend.Backend{UUID: "backend-err", Name: "err"}
			Expect(rt.PrevStore.Backends().Create(context.Background(), okBackend)).To(Succeed())
			Expect(rt.PrevStore.Backends().Create(context.Background(), errBackend)).To(Succeed())
			rt.States.AddError(errBackend, fmt.Errorf("rejected by envoy"))

			resp, err := server.ListStatus(ctx, &agentstatus.ListStatusRequest{TypeUrl: helpers.TypeUrl[*backend.Backend]()})
			Expect(err).ToNot(HaveOccurred())
			states := map[agentstatus.StatusResponse_State]int{}
			for _, st := range resp.GetStatuses() {
				states[st.GetState()]++
			}
			Expect(states).To(Equal(map[agentstatus.StatusResponse_State]int{
				agentstatus.StatusResponse_OK:      1,
				agentstatus.StatusResponse_ERRORED: 1,
			}))

			st, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: helpers.TypeUrl[*backend.Backend](), Identifier: "backend-err"})
			Expect(err).ToNot(HaveOccurred())
			Expect(st.GetState()).To(Equal(agentstatus.StatusResponse_ERRORED))
			Expect(st.GetError()).To(ContainSubstring("rejected by envoy"))
			Expect(st.GetErroredResource()).ToNot(BeNil())

			st, err = server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: helpers.TypeUrl[*backend.Backend](), Identifier: "backend-ok"})
			Expect(err).ToNot(HaveOccurred())
			Expect(st.GetState()).To(Equal(agentstatus.StatusResponse_OK))
			Expect(st.GetErroredResource()).To(BeNil())
		})

		It("should give the notes of a resource", func() {
			Expect(rt.PrevStore.VirtualServers().Create(context.Background(), &vs.VirtualServer{UUID: "vs-notes", Name: "notes"})).To(Succeed())
			rt.States.SetNotes(helpers.TypeUrl[*vs.VirtualServer](), "vs-notes", "datapath 10.0.0.1:80/tcp", []*flocktype.Note{{
				Code: "starling.datapath.ipvs", Scope: "10.0.0.1:80/tcp", Message: "served by IPVS in the kernel",
			}})

			st, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: helpers.TypeUrl[*vs.VirtualServer](), Identifier: "vs-notes"})
			Expect(err).ToNot(HaveOccurred())
			Expect(st.GetNotes()).To(ConsistOf(HaveField("Code", "starling.datapath.ipvs")))

			resp, err := server.ListStatus(ctx, &agentstatus.ListStatusRequest{TypeUrl: helpers.TypeUrl[*vs.VirtualServer]()})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetStatuses()).To(ContainElement(HaveField("Notes", ConsistOf(HaveField("Code", "starling.datapath.ipvs")))))
		})

		It("should put the secret references back in the errored resource", func() {
			backendType := helpers.TypeUrl[*backend.Backend]()
			// the ads handler writes the prev store with the references, the state store gets the resolved message
			resolved := &backend.Backend{UUID: "backend-1", Metadata: map[string]string{"a": "v"}}
			unresolved := &backend.Backend{UUID: "backend-1", Metadata: map[string]string{"a": "secret://s1/k"}}
			Expect(rt.PrevStore.Backends().Create(context.Background(), unresolved)).To(Succeed())
			rt.SecretLinks.Set(backendType, "backend-1", []secret.FoundRef{{Path: `metadata["a"]`, Ref: secret.Ref{SecretUUID: "s1", Key: "k"}}})
			rt.States.AddError(resolved, fmt.Errorf("rejected"))

			st, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: backendType, Identifier: "backend-1"})
			Expect(err).ToNot(HaveOccurred())
			previous := &backend.Backend{}
			Expect(st.GetPreviousResource().UnmarshalTo(previous)).To(Succeed())
			Expect(previous.GetMetadata()).To(Equal(map[string]string{"a": "secret://s1/k"}))
			errored := &backend.Backend{}
			Expect(st.GetErroredResource().UnmarshalTo(errored)).To(Succeed())
			Expect(errored.GetMetadata()).To(Equal(map[string]string{"a": "secret://s1/k"}))

			list, err := server.ListStatus(ctx, &agentstatus.ListStatusRequest{TypeUrl: backendType})
			Expect(err).ToNot(HaveOccurred())
			listed := &backend.Backend{}
			Expect(list.GetStatuses()[0].GetPreviousResource().UnmarshalTo(listed)).To(Succeed())
			Expect(listed.GetMetadata()).To(Equal(map[string]string{"a": "secret://s1/k"}))
			listedErrored := &backend.Backend{}
			Expect(list.GetStatuses()[0].GetErroredResource().UnmarshalTo(listedErrored)).To(Succeed())
			Expect(listedErrored.GetMetadata()).To(Equal(map[string]string{"a": "secret://s1/k"}))
		})

		It("should refuse the status of a secret, which holds values", func() {
			Expect(rt.PrevStore.Secrets().Create(context.Background(), &secret.Secret{UUID: "s1", Data: map[string]string{"k": "v"}})).
				To(Succeed())

			st, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: helpers.TypeUrl[*secret.Secret](), Identifier: "s1"})
			Expect(err).To(HaveOccurred())
			Expect(st).To(BeNil())
		})

		It("should give not found for unknown resource", func() {
			_, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: helpers.TypeUrl[*backend.Backend](), Identifier: "unknown"})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
		})

		It("should give endpoints health as status", func() {
			edpStats.stats["backend-1"] = &backend.BackendEndpointStats{
				BackendUuid: "backend-1",
				EndpointStats: []*backend.EndpointStat{
					{Healthy: true}, {Healthy: false}, {Healthy: false},
				},
			}
			edpStats.stats["backend-2"] = &backend.BackendEndpointStats{
				BackendUuid:   "backend-2",
				EndpointStats: []*backend.EndpointStat{{Healthy: true}},
			}
			typeUrl := helpers.TypeUrl[*backend.BackendEndpointStats]()

			resp, err := server.ListStatus(ctx, &agentstatus.ListStatusRequest{TypeUrl: typeUrl})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetStatuses()).To(HaveLen(2))

			st, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: typeUrl, Identifier: "backend-1"})
			Expect(err).ToNot(HaveOccurred())
			Expect(st.GetState()).To(Equal(agentstatus.StatusResponse_ERRORED))
			Expect(st.GetError()).To(Equal("there is 2 endpoints in error"))

			st, err = server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: typeUrl, Identifier: "backend-2"})
			Expect(err).ToNot(HaveOccurred())
			Expect(st.GetState()).To(Equal(agentstatus.StatusResponse_OK))

			_, err = server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: typeUrl, Identifier: "unknown"})
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
		})
	})

	Context("agent status", func() {
		It("should report envoy state", func() {
			envoyAlive = fmt.Errorf("envoy is dead")
			resp, err := server.GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{})
			Expect(err).ToNot(HaveOccurred())
			state := resp.GetSidecarAgentState()
			Expect(state.GetAgentState().GetName()).To(Equal("node-1"))
			Expect(state.GetComponentStates()).To(ContainElement(And(
				HaveField("Name", "envoy"),
				HaveField("Status", agent.Status_ERRORED),
				HaveField("Details", "envoy is dead"),
			)))
			Expect(state.GetAnnouncingVips()).To(BeEmpty())
		})

		It("should report the WAF state", func() {
			wafAlive = fmt.Errorf("starling-waf health check: connection refused")
			resp, err := server.GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetSidecarAgentState().GetComponentStates()).To(ContainElements(
				And(HaveField("Name", "envoy"), HaveField("Status", agent.Status_OK)),
				And(
					HaveField("Name", "waf"),
					HaveField("Status", agent.Status_ERRORED),
					HaveField("Details", "starling-waf health check: connection refused"),
				),
			))
		})

		It("should give all vips as announcing without placer", func() {
			Expect(rt.Store.VIPs().Create(context.Background(), announcedVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			resp, err := server.GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{ShowAnnouncingVips: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetSidecarAgentState().GetAnnouncingVips()).To(HaveLen(1))
		})

		It("should put the secret references back in the announcing vips", func() {
			announced := announcedVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2)
			announced.Metadata = map[string]string{"a": "v"}
			Expect(rt.Store.VIPs().Create(context.Background(), announced)).To(Succeed())
			rt.SecretLinks.Set(helpers.TypeUrl[*vip.VIP](), "vip-1", []secret.FoundRef{{Path: `metadata["a"]`, Ref: secret.Ref{SecretUUID: "s1", Key: "k"}}})

			resp, err := server.GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{ShowAnnouncingVips: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetSidecarAgentState().GetAnnouncingVips()[0].GetMetadata()).To(Equal(map[string]string{"a": "secret://s1/k"}))
		})

		It("should only give vips placed on this node with placer", func() {
			Expect(rt.Store.VIPs().Create(context.Background(), announcedVip("vip-l2", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			Expect(rt.Store.VIPs().Create(context.Background(), announcedVip("vip-bgp", "10.0.0.2", starling_announce.StarlingAnnounce_BGP))).To(Succeed())
			placer = agentlist.NewVipPlacer(context.Background(), "node-1", nil, nil, rt.Store.VIPs())
			placer.AddPlaceOn(starling_announce.StarlingAnnounce_LAYER2)
			server = newServer()

			// not placed yet: only vips not handled by placer are announced
			resp, err := server.GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{ShowAnnouncingVips: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetSidecarAgentState().GetAnnouncingVips()).To(ConsistOf(HaveField("UUID", "vip-bgp")))

			Expect(placer.RecalculateAndApply(context.Background())).To(Succeed())
			resp, err = server.GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{ShowAnnouncingVips: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetSidecarAgentState().GetAnnouncingVips()).To(HaveLen(2))
		})

		It("should expose status as prometheus metrics", func() {
			Expect(rt.Store.VIPs().Create(context.Background(), announcedVip("vip-1", "10.0.0.1", starling_announce.StarlingAnnounce_LAYER2))).To(Succeed())
			registry := prometheus.NewPedanticRegistry()
			Expect(registry.Register(server)).To(Succeed())
			Expect(testutil.CollectAndCount(server, "flock_agent_announce_vips")).To(Equal(1))
			Expect(testutil.CollectAndCount(server, "flock_agent_status_components")).To(Equal(3))

			wafAlive = fmt.Errorf("dead")
			Expect(testutil.CollectAndCompare(server, strings.NewReader(`
# HELP flock_agent_status Agent status
# TYPE flock_agent_status gauge
flock_agent_status{name="node-1"} 2
`), "flock_agent_status")).To(Succeed())
		})
	})

	Context("UpdateServerCertificate", func() {
		It("should write certificate, key and ca", func() {
			certPem, keyPem := generateCertAndKey()
			caPem, _ := generateCertAndKey()
			tlsInfo.RootCaPool = nil
			server = newServer()

			_, err := server.UpdateServerCertificate(ctx, &agentmgmt.UpdateServerCertificateRequest{
				CertChain: certPem, PrivateKey: keyPem, Ca: caPem,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(os.ReadFile(tlsInfo.CertFilePath)).To(BeEquivalentTo(certPem))
			Expect(os.ReadFile(tlsInfo.KeyFilePath)).To(BeEquivalentTo(keyPem))
			keyStat, err := os.Stat(tlsInfo.KeyFilePath)
			Expect(err).ToNot(HaveOccurred())
			Expect(keyStat.Mode().Perm()).To(Equal(os.FileMode(0600)), "private key must not be world readable")
			// ca is only written when mtls is used (root ca pool set)
			Expect(tlsInfo.CAFilePath).ToNot(BeAnExistingFile())
		})

		DescribeTable("should reject invalid requests",
			func(req func() *agentmgmt.UpdateServerCertificateRequest, errSubstring string) {
				_, err := server.UpdateServerCertificate(ctx, req())
				Expect(connect.CodeOf(err)).To(Equal(connect.CodeInvalidArgument))
				Expect(err).To(MatchError(ContainSubstring(errSubstring)))
				Expect(tlsInfo.CertFilePath).ToNot(BeAnExistingFile())
			},
			Entry("empty", func() *agentmgmt.UpdateServerCertificateRequest {
				return &agentmgmt.UpdateServerCertificateRequest{}
			}, "no certificate or CA provided"),
			Entry("invalid ca", func() *agentmgmt.UpdateServerCertificateRequest {
				return &agentmgmt.UpdateServerCertificateRequest{Ca: "not a pem"}
			}, "invalid ca"),
			Entry("invalid chain", func() *agentmgmt.UpdateServerCertificateRequest {
				_, keyPem := generateCertAndKey()
				return &agentmgmt.UpdateServerCertificateRequest{CertChain: "not a pem", PrivateKey: keyPem}
			}, "invalid certChain"),
			Entry("invalid key", func() *agentmgmt.UpdateServerCertificateRequest {
				certPem, _ := generateCertAndKey()
				return &agentmgmt.UpdateServerCertificateRequest{CertChain: certPem, PrivateKey: "not a pem"}
			}, "invalid privateKey"),
			Entry("cert without key", func() *agentmgmt.UpdateServerCertificateRequest {
				certPem, _ := generateCertAndKey()
				return &agentmgmt.UpdateServerCertificateRequest{CertChain: certPem}
			}, "cert and key must be provided together"),
		)
	})

	Context("logs", func() {
		logLine := func(line string) {
			_, err := logHook.Write([]byte(line + "\n"))
			Expect(err).ToNot(HaveOccurred())
		}

		It("should give latest log lines", func() {
			logLine("first line")
			logLine("second line")

			resp, err := server.Latest(ctx, &emptypb.Empty{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetLogLines()).To(HaveLen(2))
			Expect(resp.GetLogLines()[1]).To(ContainSubstring("second line"))
		})

		// follow serves a Follow call through a connect server, as a client would
		follow := func(clientCtx context.Context, stream *fakeFollowServer) chan error {
			connectServer := connect.NewServer()
			agentlogsconnect.RegisterLogsHandler(connectServer, server)
			done := make(chan error, 1)
			go func() {
				done <- connectServer.Call(clientCtx, agentlogsconnect.LogsFollowProcedure, nil, stream)
			}()
			return done
		}

		It("should stream new log lines without concurrent send and stop when client leaves", func() {
			clientCtx, clientLeaves := context.WithCancel(context.Background())
			stream := &fakeFollowServer{}
			done := follow(clientCtx, stream)

			// wait listener to be registered
			Eventually(func() int {
				logLine("ping")
				return len(stream.Lines())
			}, 5*time.Second).Should(BeNumerically(">", 0))

			for i := range 50 {
				logLine(fmt.Sprintf("line %d", i))
			}
			Eventually(func() []string { return stream.Lines() }, 5*time.Second).Should(ContainElement(ContainSubstring("line 49")))
			Consistently(done, 100*time.Millisecond).ShouldNot(Receive(), "concurrent send would make Follow fail")

			clientLeaves()
			Eventually(done, 5*time.Second).Should(Receive(BeNil()))
		})

		It("should send a started message before any line", func() {
			clientCtx, clientLeaves := context.WithCancel(context.Background())
			DeferCleanup(clientLeaves)
			stream := &fakeFollowServer{}
			follow(clientCtx, stream)

			Eventually(func() bool {
				stream.mutex.Lock()
				defer stream.mutex.Unlock()
				return stream.startedFirst
			}, 5*time.Second).Should(BeTrue())
		})

		It("should fail when sending to client fails", func() {
			stream := &fakeFollowServer{errSend: fmt.Errorf("broken pipe")}
			done := follow(context.Background(), stream)
			Eventually(func() int {
				logLine("ping")
				return len(done)
			}, 5*time.Second).Should(Equal(1))
			Expect(connect.CodeOf(<-done)).To(Equal(connect.CodeInternal))
		})
	})
})
