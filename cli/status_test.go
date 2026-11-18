package cli

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("status", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcinstanceconnect.InstanceReadInstanceProcedure, reply(&svcinstance.InstanceResponse{
			Instance: &instance.Instance{UUID: resourceUuid, Name: "lb"},
		}))
		runner.conn.Handle(svclookupconnect.LookupListAgentProcedure, reply(&svclookup.ListAgentResponse{
			Agents: []*agent.Agent{{UUID: "agent-1"}, {UUID: "agent-2"}},
		}))
	})

	It("requires a resource type", func() {
		_, err := runner.run("status", resourceUuid)

		Expect(err).To(MatchError(ContainSubstring("--resource-type' was not specified")))
	})

	It("returns the cancellation instead of one errored row per agent when its context ends", func() {
		runner.conn.Handle(svclookupconnect.LookupListStatusProcedure, func(proto.Message) (proto.Message, error) {
			runner.cancel()
			return nil, context.Canceled
		})

		out, err := runner.run("status", "-r", "vip", resourceUuid)

		Expect(err).To(MatchError(context.Canceled))
		Expect(out).NotTo(ContainSubstring("ERRORED"))
	})

	It("lists the status of every resource of the type on every agent", func() {
		runner.conn.Handle(svclookupconnect.LookupListStatusProcedure, func(req proto.Message) (proto.Message, error) {
			agentUuid := req.(*svclookup.ListStatusRequest).GetAgentUuid()
			return &svclookup.ListStatusResponse{Status: []*svclookup.StatusResponse{{
				AgentUuid:        agentUuid,
				State:            svclookup.StatusResponse_OK,
				PreviousResource: packAny(&vip.VIP{Name: "public"}),
			}}}, nil
		})

		out, err := runner.run("status", "-r", "vip", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("agent-1"))
		Expect(out).To(ContainSubstring("agent-2"))
		Expect(out).To(ContainSubstring("public"))
		Expect(out).To(ContainSubstring("There is no error"))
		sent := lastRequest[*svclookup.ListStatusRequest](runner.conn, svclookupconnect.LookupListStatusProcedure)
		Expect(sent.GetInstanceUuid()).To(Equal(resourceUuid))
		Expect(sent.GetTypeUrl()).To(Equal(helpers.TypeUrl[*vip.VIP]()))
	})

	Describe("provisioners", func() {
		BeforeEach(func() {
			runner.conn.Handle(svclookupconnect.LookupListAgentProcedure, reply(&svclookup.ListAgentResponse{
				Agents: []*agent.Agent{
					{UUID: "agent-1"},
					{UUID: "acme-1", Mode: agent.Agent_PROVISIONER},
				},
			}))
			runner.conn.Handle(svclookupconnect.LookupListStatusProcedure, func(req proto.Message) (proto.Message, error) {
				return &svclookup.ListStatusResponse{Status: []*svclookup.StatusResponse{{
					AgentUuid:        req.(*svclookup.ListStatusRequest).GetAgentUuid(),
					State:            svclookup.StatusResponse_OK,
					PreviousResource: packAny(&vip.VIP{Name: "public"}),
				}}}, nil
			})
		})

		askedAgents := func() []string {
			requests := runner.conn.Requests(svclookupconnect.LookupListStatusProcedure)
			uuids := make([]string, 0, len(requests))
			for _, req := range requests {
				uuids = append(uuids, req.(*svclookup.ListStatusRequest).GetAgentUuid())
			}
			return uuids
		}

		reporting := func(types ...string) {
			runner.conn.Handle(svclookupconnect.LookupListResourceTypesStatusProcedure,
				reply(&svclookup.ListResourceTypesStatusResponse{ResourceTypes: types}))
		}

		It("asks the ones reporting the type and prints them last", func() {
			reporting(helpers.TypeUrl[*vip.VIP](), helpers.TypeUrl[*cert.Certificate]())

			out, err := runner.run("status", "-r", "vip", resourceUuid)

			Expect(err).NotTo(HaveOccurred())
			Expect(askedAgents()).To(ConsistOf("agent-1", "acme-1"))
			Expect(out).To(ContainSubstring("acme-1 (provisioner)"))
			Expect(strings.Index(out, "agent-1")).To(BeNumerically("<", strings.Index(out, "acme-1")))
			types := lastRequest[*svclookup.ListResourceTypesRequest](runner.conn, svclookupconnect.LookupListResourceTypesStatusProcedure)
			Expect(types.GetAgentUuid()).To(Equal("acme-1"))
		})

		It("does not ask the ones not reporting the type", func() {
			reporting(helpers.TypeUrl[*cert.Certificate]())

			_, err := runner.run("status", "-r", "vip", resourceUuid)

			Expect(err).NotTo(HaveOccurred())
			Expect(askedAgents()).To(ConsistOf("agent-1"))
		})

		It("keeps a provisioner whose types can't be read", func() {
			runner.conn.Handle(svclookupconnect.LookupListResourceTypesStatusProcedure,
				failWith(connect.NewError(connect.CodeUnavailable, "provisioner down")))

			_, err := runner.run("status", "-r", "vip", resourceUuid)

			Expect(err).NotTo(HaveOccurred())
			Expect(askedAgents()).To(ConsistOf("agent-1", "acme-1"))
		})
	})

	It("gets the status of one resource found by name on the given agent", func() {
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, reply(&svcvip.ListVIPResponse{Vip: []*vip.VIP{{UUID: "vip-1", Name: "public"}}}))
		runner.conn.Handle(svclookupconnect.LookupGetStatusProcedure, reply(&svclookup.StatusResponse{
			AgentUuid:        "agent-9",
			State:            svclookup.StatusResponse_ERRORED,
			Error:            "bind failed",
			PreviousResource: packAny(&vip.VIP{Name: "public", Ip: "10.0.0.1"}),
			ErroredResource:  packAny(&vip.VIP{Name: "public", Ip: "10.0.0.2"}),
		}))

		out, err := runner.run("status", "-r", "vip", "-i", "public", "-a", "agent-9", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("ERRORED"))
		Expect(out).To(ContainSubstring("bind failed"))
		Expect(out).To(ContainSubstring("10.0.0.2"))
		sent := lastRequest[*svclookup.GetStatusRequest](runner.conn, svclookupconnect.LookupGetStatusProcedure)
		Expect(sent.GetIdentifier()).To(Equal("vip-1"))
		Expect(sent.GetAgentUuid()).To(Equal("agent-9"))
		Expect(runner.conn.Requests(svclookupconnect.LookupListAgentProcedure)).To(BeEmpty())
	})

	It("prints the notes of an applied resource", func() {
		runner.conn.Handle(svclookupconnect.LookupListStatusProcedure, reply(&svclookup.ListStatusResponse{Status: []*svclookup.StatusResponse{{
			AgentUuid:        "agent-1",
			State:            svclookup.StatusResponse_OK,
			PreviousResource: packAny(&vip.VIP{Name: "public"}),
			Notes: []*flocktype.Note{
				{Level: flocktype.Note_WARNING, Code: "f5.approximation", Message: "maglev not supported"},
				{Level: flocktype.Note_INFO, Code: "starling.datapath.ipvs", Scope: "10.0.0.1:80/tcp", Message: "served by IPVS in the kernel"},
			},
		}}}))

		out, err := runner.run("status", "-r", "vip", "-a", "agent-1", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("warning: maglev not supported"))
		Expect(out).To(ContainSubstring("note: served by IPVS in the kernel (10.0.0.1:80/tcp)"))
	})

	It("prints the error then the notes", func() {
		runner.conn.Handle(svclookupconnect.LookupListStatusProcedure, reply(&svclookup.ListStatusResponse{Status: []*svclookup.StatusResponse{{
			AgentUuid:        "agent-1",
			State:            svclookup.StatusResponse_ERRORED,
			PreviousResource: packAny(&vip.VIP{Name: "public"}),
			Error:            "boom",
			Notes:            []*flocktype.Note{{Level: flocktype.Note_WARNING, Code: "f5.approximation", Message: "maglev not supported"}},
		}}}))

		out, err := runner.run("status", "-r", "vip", "-a", "agent-1", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(MatchRegexp(`(?s)boom.*warning: maglev not supported`))
	})

	It("reports agents that cannot answer instead of failing", func() {
		runner.conn.Handle(svclookupconnect.LookupListStatusProcedure, failWith(connect.NewError(connect.CodeUnavailable, "agent down")))

		out, err := runner.run("status", "-r", "backend", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Agent agent-1 in error"))
		Expect(out).To(ContainSubstring("agent down"))
	})

	It("goes through every instance when none is given", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, firstPage(&svcinstance.ListInstanceResponse{
			Instances: []*instance.Instance{{UUID: "inst-1", Name: "first"}, {UUID: "inst-2", Name: "second"}},
		}))
		runner.conn.Handle(svclookupconnect.LookupListStatusProcedure, reply(&svclookup.ListStatusResponse{}))

		out, err := runner.run("status", "-r", "vip")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Status in Instance first"))
		Expect(out).To(ContainSubstring("Status in Instance second"))
	})

	It("prints the endpoints of each backend with the virtual servers using it", func() {
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{Vip: []*vip.VIP{{UUID: "vip-1", Ip: "10.0.0.1"}}}))
		runner.conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{Vs: []*vs.VirtualServer{tcpVs("vs-1", "web", 443, "backend-1")}}))
		runner.conn.Handle(svclookupconnect.LookupListStatusProcedure, reply(&svclookup.ListStatusResponse{Status: []*svclookup.StatusResponse{{
			AgentUuid:        "agent-1",
			State:            svclookup.StatusResponse_OK,
			PreviousResource: packAny(endpointStats),
		}}}))

		out, err := runner.run("status", "-r", "endpoints", "--no-interact", "-a", "agent-1", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("10.0.0.1:443/web"))
		Expect(out).To(ContainSubstring("10.0.0.5:8080"))
		Expect(out).To(ContainSubstring("Healthy"))
		Expect(out).To(ContainSubstring("Unhealthy"))
		Expect(out).To(ContainSubstring("connection refused"))
		Expect(out).To(ContainSubstring("Warning"))
	})

	It("prints the statuses as json with --json", func() {
		runner.conn.Handle(svclookupconnect.LookupListStatusProcedure, reply(&svclookup.ListStatusResponse{Status: []*svclookup.StatusResponse{{
			AgentUuid: "agent-1",
			State:     svclookup.StatusResponse_OK,
		}}}))

		out, err := runner.run("status", "-r", "vip", "--json", "-a", "agent-1", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).To(ConsistOf(HaveKeyWithValue("agent_uuid", "agent-1")))
	})

	DescribeTable("status-<type> commands set the resource type",
		func(command, wantTypeUrl string) {
			runner.conn.Handle(svclookupconnect.LookupListStatusProcedure, reply(&svclookup.ListStatusResponse{}))
			// Endpoints are shown with the virtual servers using their backend, found from the vips.
			runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{}))

			_, err := runner.run(command, "-a", "agent-1", resourceUuid)

			Expect(err).NotTo(HaveOccurred())
			sent := lastRequest[*svclookup.ListStatusRequest](runner.conn, svclookupconnect.LookupListStatusProcedure)
			Expect(sent.GetTypeUrl()).To(Equal(wantTypeUrl))
		},
		Entry("certificates", "status-certificates", helpers.TypeUrl[*cert.Certificate]()),
		Entry("backends", "status-backends", helpers.TypeUrl[*backend.Backend]()),
		Entry("vips", "status-vips", helpers.TypeUrl[*vip.VIP]()),
		Entry("virtual servers", "status-vs", helpers.TypeUrl[*vs.VirtualServer]()),
		Entry("endpoints", "status-endpoints", helpers.TypeUrl[*backend.BackendEndpointStats]()),
	)

	Describe("printAndClearAgentErrors", func() {
		It("removes agent level errors and keeps resource responses", func() {
			agentError := &svclookup.StatusResponse{
				AgentUuid: "agent-1",
				State:     svclookup.StatusResponse_ERRORED,
				Error:     "rpc error: code = Unavailable desc = agent down",
			}
			okResponse := &svclookup.StatusResponse{AgentUuid: "agent-3"}

			var kept []*svclookup.StatusResponse
			out := captureOutput(func() {
				kept = (&Status{}).printAndClearAgentErrors([]*svclookup.StatusResponse{agentError, okResponse})
			})

			Expect(kept).To(Equal([]*svclookup.StatusResponse{okResponse}))
			Expect(out).To(ContainSubstring("Agent agent-1 in error: agent down (Code: Unavailable)"))
		})
	})
})
