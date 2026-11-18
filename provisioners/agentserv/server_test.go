package agentserv_test

import (
	"context"
	"errors"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/seagalia/flock/provisioners/agentserv"
	"github.com/seagalia/flock/sdk/agentsdk/loghooks"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("Server", func() {
	var (
		rt         *runtime.Runtime
		stateStore *stores.StateStore
		logHook    *loghooks.ListenerWriter
		server     *agentserv.Server
		crt        *cert.Certificate
		certType   = helpers.TypeUrl[*cert.Certificate]()
		ctx        = context.Background()
	)

	BeforeEach(func() {
		rt = runtime.New()
		stateStore = rt.States
		logHook = loghooks.NewListenerWriter()
		server = agentserv.NewServer("prov-1", rt.PrevStore, stateStore, rt.SecretLinks, logHook, true)
		crt = &cert.Certificate{UUID: "cert-1", Name: "shop", DomainNames: []string{"shop.example.com"}}
		Expect(rt.PrevStore.Certificates().Create(context.Background(), crt)).To(Succeed())
	})

	Describe("GetStatus", func() {
		It("answers the state of a certificate the state store holds", func() {
			stateStore.AddError(crt, errors.New("ca down"))

			status, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: certType, Identifier: "cert-1"})

			Expect(err).NotTo(HaveOccurred())
			Expect(status.GetState()).To(Equal(agentstatus.StatusResponse_ERRORED))
			Expect(status.GetError()).To(Equal("ca down"))
			previous := &cert.Certificate{}
			Expect(status.GetPreviousResource().UnmarshalTo(previous)).To(Succeed())
			Expect(previous.GetUUID()).To(Equal("cert-1"))
		})

		It("answers OK for a certificate the provisioner holds with no state", func() {
			status, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: certType, Identifier: "cert-1"})

			Expect(err).NotTo(HaveOccurred())
			Expect(status.GetState()).To(Equal(agentstatus.StatusResponse_OK))
		})

		It("answers NotFound for a certificate the provisioner doesn't hold", func() {
			_, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: certType, Identifier: "cert-2"})

			Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
		})
	})

	It("lists the status of every certificate it holds", func() {
		stateStore.SetState(crt, agentstatus.StatusResponse_ADDING)

		statuses, err := server.ListStatus(ctx, &agentstatus.ListStatusRequest{TypeUrl: certType})

		Expect(err).NotTo(HaveOccurred())
		Expect(statuses.GetStatuses()).To(HaveLen(1))
		Expect(statuses.GetStatuses()[0].GetState()).To(Equal(agentstatus.StatusResponse_ADDING))
	})

	It("gives the certificates as its only resource type", func() {
		types, err := server.ListResourceTypes(ctx, &emptypb.Empty{})

		Expect(err).NotTo(HaveOccurred())
		Expect(types.GetResourceTypes()).To(ConsistOf(certType))
	})

	Describe("GetAgentStatus", func() {
		It("answers the provisioner with one component acme", func() {
			status, err := server.GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{})

			Expect(err).NotTo(HaveOccurred())
			sidecar := status.GetSidecarAgentState()
			Expect(sidecar.GetAgentState().GetName()).To(Equal("prov-1"))
			Expect(sidecar.GetAgentState().GetStatus()).To(Equal(agent.Status_OK))
			Expect(sidecar.GetComponentStates()).To(HaveLen(1))
			Expect(sidecar.GetComponentStates()[0].GetName()).To(Equal("acme"))
			Expect(sidecar.GetComponentStates()[0].GetStatus()).To(Equal(agent.Status_OK))
		})

		It("reports no component when acme is disabled", func() {
			server = agentserv.NewServer("prov-1", rt.PrevStore, stateStore, rt.SecretLinks, logHook, false)

			status, err := server.GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{})

			Expect(err).NotTo(HaveOccurred())
			Expect(status.GetSidecarAgentState().GetAgentState().GetName()).To(Equal("prov-1"))
			Expect(status.GetSidecarAgentState().GetComponentStates()).To(BeEmpty())
		})

		It("warns on the acme component while a certificate is in error", func() {
			stateStore.AddError(crt, errors.New("ca down"))

			status, err := server.GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{})

			Expect(err).NotTo(HaveOccurred())
			acme := status.GetSidecarAgentState().GetComponentStates()[0]
			Expect(acme.GetStatus()).To(Equal(agent.Status_WARNING))
			Expect(acme.GetDetails()).To(ContainSubstring("cert-1"))
		})
	})

	It("gives the latest lines written to the log hook", func() {
		_, err := logHook.Write([]byte("a log line\n"))
		Expect(err).NotTo(HaveOccurred())

		latest, err := server.Latest(ctx, &emptypb.Empty{})

		Expect(err).NotTo(HaveOccurred())
		Expect(latest.GetLogLines()).To(ContainElement(ContainSubstring("a log line")))
	})
})
