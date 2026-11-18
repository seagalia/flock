package cli

import (
	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("vips", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("prints every vip in a table", func() {
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{Vip: []*vip.VIP{
			{Name: "public", Ip: "10.0.0.1"},
			{Name: "private", Ip: "10.0.0.2"},
		}}))

		out, err := runner.run("vips")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("public"))
		Expect(out).To(ContainSubstring("10.0.0.1"))
		Expect(out).To(ContainSubstring("private"))
		Expect(out).To(ContainSubstring("10.0.0.2"))
		// The second call returns an empty page and stops the pagination.
		Expect(runner.conn.Requests(svcvipconnect.VIPListVIPProcedure)).To(HaveLen(2))
	})

	It("says when there is no vip", func() {
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{}))

		out, err := runner.run("vips")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("No VIPs found."))
	})

	It("sends metadata filters given with --filter", func() {
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{}))

		_, err := runner.run("vips", "--filter", "env==prod", "--filter", "team~=net")

		Expect(err).NotTo(HaveOccurred())
		filters := sentFilters(runner, svcvipconnect.VIPListVIPProcedure)
		Expect(filters).To(HaveLen(2))
		Expect(filters[0].GetKey()).To(Equal("env"))
		Expect(filters[0].GetIsMetadata()).To(BeTrue())
		Expect(filters[1].GetOperator()).To(Equal(svccore.ListFilter_LIKE))
	})

	It("lists the vips of an instance found by name with --instance", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, reply(&svcinstance.ListInstanceResponse{
			Instances: []*instance.Instance{{UUID: resourceUuid, Name: "lb"}},
		}))
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{}))

		_, err := runner.run("vips", "--instance", "lb")

		Expect(err).NotTo(HaveOccurred())
		filters := sentFilters(runner, svcvipconnect.VIPListVIPProcedure)
		Expect(filters).To(ContainElement(HaveField("Key", helpers.TypeUrl[*instance.Instance]())))
		Expect(filters).To(ContainElement(HaveField("Value", resourceUuid)))
	})

	It("shows parent instances and rd uuid with --show-parent", func() {
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{Vip: []*vip.VIP{
			{UUID: "vip-1", Name: "public", Metadata: map[string]string{"rdUuid": "rd-42"}},
			{UUID: "vip-2", Name: "orphan"},
		}}))
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, func(req proto.Message) (proto.Message, error) {
			vipUuid := req.(*svcinstance.ListInstanceRequest).GetFilters()[0].GetValue()
			if vipUuid == "vip-1" {
				return &svcinstance.ListInstanceResponse{Instances: []*instance.Instance{{Name: "lb"}}}, nil
			}
			return &svcinstance.ListInstanceResponse{}, nil
		})

		out, err := runner.run("vips", "--show-parent")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("rd-42"))
		Expect(out).To(ContainSubstring("lb"))
		Expect(out).To(ContainSubstring("Not used"))
		Expect(out).To(ContainSubstring("not set"))
	})

	It("prints a json list with --json", func() {
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{Vip: []*vip.VIP{{Name: "public"}}}))

		out, err := runner.run("vips", "--json")

		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).To(ConsistOf(HaveKeyWithValue("name", "public")))
	})

	It("returns the server error", func() {
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, failWith(connect.NewError(connect.CodeUnavailable, "down")))

		_, err := runner.run("vips")

		Expect(err).To(MatchError(ContainSubstring("unable to list vips")))
	})
})
