package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("vips of an instance", func() {
	const vipUuid = "22222222-3333-4444-5555-666666666666"
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcinstanceconnect.InstanceReadInstanceProcedure, reply(&svcinstance.InstanceResponse{Instance: &instance.Instance{UUID: resourceUuid}}))
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, reply(&svcvip.ListVIPResponse{Vip: []*vip.VIP{{UUID: vipUuid, Name: "public"}}}))
	})

	It("adds a vip found by its name to an instance", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceAddVipToInstanceProcedure, reply(&emptypb.Empty{}))

		out, err := runner.run("add-vip-to-instance", resourceUuid, "public")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Vip successfully added."))
		sent := lastRequest[*svcinstance.AddVipToInstanceRequest](runner.conn, svcinstanceconnect.InstanceAddVipToInstanceProcedure)
		Expect(sent.GetUUID()).To(Equal(resourceUuid))
		Expect(sent.GetVipUuid()).To(Equal(vipUuid))
	})

	It("deletes a vip from an instance", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceDeleteVipFromInstanceProcedure, reply(&emptypb.Empty{}))

		out, err := runner.run("delete-vip-from-instance", resourceUuid, "public")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Vip successfully deleted."))
		sent := lastRequest[*svcinstance.DeleteVipFromInstanceRequest](runner.conn, svcinstanceconnect.InstanceDeleteVipFromInstanceProcedure)
		Expect(sent.GetVipUuid()).To(Equal(vipUuid))
	})
})
