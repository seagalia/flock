package cli

import (
	"context"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

// statusOfType answers like an agent: the status carries the configuration of the requested type.
func statusOfType(req proto.Message) (proto.Message, error) {
	statusReq := req.(*svclookup.GetStatusRequest)
	response := &svclookup.StatusResponse{AgentUuid: statusReq.GetAgentUuid(), State: svclookup.StatusResponse_OK}
	switch statusReq.GetTypeUrl() {
	case helpers.TypeUrl[*backend.BackendEndpointStats]():
		response.PreviousResource = packAny(endpointStats)
	case helpers.TypeUrl[*vs.VirtualServer]():
		response.PreviousResource = packAny(&vs.VirtualServer{Name: "web"})
	case helpers.TypeUrl[*backend.Backend]():
		response.PreviousResource = packAny(&backend.Backend{Name: "backend"})
	default:
		response.PreviousResource = packAny(&vip.VIP{Name: "public"})
	}
	return response, nil
}

var _ = Describe("check", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{Vip: []*vip.VIP{
			{UUID: "vip-other", Ip: "10.9.9.9"},
			{UUID: "vip-1", Name: "public", Ip: "10.0.0.1"},
			{UUID: "vip-6", Name: "public6", Ip: "2001:db8::1"},
		}}))
		runner.conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{Vs: []*vs.VirtualServer{
			tcpVs("vs-1", "web", 443, "backend-1"),
		}}))
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, firstPage(&svcinstance.ListInstanceResponse{
			Instances: []*instance.Instance{{UUID: resourceUuid, Name: "lb"}},
		}))
		runner.conn.Handle(svccertconnect.CertificateListCertificateProcedure, firstPage(&svccert.ListCertificateResponse{}))
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure, firstPage(&svcbackend.ListBackendResponse{
			Backends: []*backend.Backend{{UUID: "backend-1", Name: "backend"}},
		}))
		runner.conn.Handle(svclookupconnect.LookupListAgentProcedure, reply(&svclookup.ListAgentResponse{
			Agents: []*agent.Agent{{UUID: "agent-1"}},
		}))
		runner.conn.Handle(svclookupconnect.LookupGetStatusProcedure, statusOfType)
	})

	It("checks every resource behind an ip and port", func() {
		out, err := runner.run("check", "10.0.0.1:443")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Found 10.0.0.1 on instance lb"))
		Expect(out).To(ContainSubstring("Vip(s) check configuration result"))
		Expect(out).To(ContainSubstring("Virtual server(s) check configuration result"))
		Expect(out).To(ContainSubstring("Backend(s) check configuration result"))
		Expect(out).To(ContainSubstring("Endpoints status result"))
		Expect(out).To(ContainSubstring("web/10.0.0.1:443"))
		Expect(out).To(ContainSubstring("10.0.0.5:8080"))
	})

	It("prints the error of every failing agent before the result tables", func() {
		runner.conn.Handle(svclookupconnect.LookupListAgentProcedure, reply(&svclookup.ListAgentResponse{
			Agents: []*agent.Agent{{UUID: "agent-1"}, {UUID: "agent-2"}, {UUID: "agent-3"}},
		}))
		runner.conn.Handle(svclookupconnect.LookupGetStatusProcedure, func(req proto.Message) (proto.Message, error) {
			if req.(*svclookup.GetStatusRequest).GetAgentUuid() != "agent-1" {
				return nil, connect.NewError(connect.CodeUnavailable, "agent is down")
			}
			return statusOfType(req)
		})

		out, err := runner.run("check", "10.0.0.1:443")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(MatchRegexp(`(?s)Vip\(s\) check configuration result.*on agent agent-2.*on agent agent-3.*public.*Virtual server\(s\)`))
	})

	It("returns the cancellation instead of one error per agent when its context ends", func() {
		runner.cancel()

		out, err := runner.run("check", "10.0.0.1:443")

		Expect(err).To(MatchError(context.Canceled))
		Expect(out).NotTo(ContainSubstring("on agent"))
	})

	Describe("provisioners", func() {
		askedAgents := func() []string {
			requests := runner.conn.Requests(svclookupconnect.LookupGetStatusProcedure)
			uuids := make([]string, 0, len(requests))
			for _, req := range requests {
				uuids = append(uuids, req.(*svclookup.GetStatusRequest).GetAgentUuid())
			}
			return uuids
		}

		BeforeEach(func() {
			runner.conn.Handle(svclookupconnect.LookupListAgentProcedure, reply(&svclookup.ListAgentResponse{
				Agents: []*agent.Agent{{UUID: "agent-1"}, {UUID: "acme-1", Mode: agent.Agent_PROVISIONER}},
			}))
		})

		It("asks a provisioner for the types it reports", func() {
			runner.conn.Handle(svclookupconnect.LookupListResourceTypesStatusProcedure,
				reply(&svclookup.ListResourceTypesStatusResponse{ResourceTypes: []string{helpers.TypeUrl[*vip.VIP]()}}))

			_, err := runner.run("check", "10.0.0.1:443")

			Expect(err).NotTo(HaveOccurred())
			Expect(askedAgents()).To(ContainElement("acme-1"))
			for _, req := range runner.conn.Requests(svclookupconnect.LookupGetStatusProcedure) {
				status := req.(*svclookup.GetStatusRequest)
				if status.GetAgentUuid() == "acme-1" {
					Expect(status.GetTypeUrl()).To(Equal(helpers.TypeUrl[*vip.VIP]()))
				}
			}
		})

		It("does not ask a provisioner for the types it does not report", func() {
			runner.conn.Handle(svclookupconnect.LookupListResourceTypesStatusProcedure,
				reply(&svclookup.ListResourceTypesStatusResponse{ResourceTypes: []string{helpers.TypeUrl[*cert.Certificate]()}}))

			_, err := runner.run("check", "10.0.0.1:443")

			Expect(err).NotTo(HaveOccurred())
			Expect(askedAgents()).NotTo(ContainElement("acme-1"))
		})
	})

	It("checks an ipv6 vip and port", func() {
		out, err := runner.run("check", "[2001:db8::1]:443")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Found 2001:db8::1 on instance lb"))
	})

	It("checks a vip without port", func() {
		out, err := runner.run("check", "10.0.0.1")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Found 10.0.0.1 on instance lb"))
	})

	It("warns when no virtual server listens on the port", func() {
		out, err := runner.run("check", "10.0.0.1:8080")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("No virtual server with port 8080 found"))
	})

	It("fails when no vip has the ip", func() {
		_, err := runner.run("check", "192.168.1.1:443")

		Expect(err).To(MatchError(ContainSubstring("no vip found for 192.168.1.1:443")))
	})

	It("tells when the vip is attached to no instance", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, firstPage(&svcinstance.ListInstanceResponse{}))

		out, err := runner.run("check", "10.0.0.1:443")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("not attached to any instance"))
	})
})
