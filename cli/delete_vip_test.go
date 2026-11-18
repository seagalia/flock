package cli

import (
	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("delete-vip", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcvipconnect.VIPDeleteVIPProcedure, reply(&emptypb.Empty{}))
	})

	It("deletes the vip found by uuid", func() {
		runner.conn.Handle(svcvipconnect.VIPReadVIPProcedure, reply(&svcvip.VIPResponse{Vip: &vip.VIP{UUID: resourceUuid}}))

		out, err := runner.run("delete-vip", "--force", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("VIP successfully deleted"))
		Expect(lastRequest[*svccore.UuidRequest](runner.conn, svcvipconnect.VIPDeleteVIPProcedure).GetUUID()).To(Equal(resourceUuid))
	})

	It("does not delete when the vip cannot be found", func() {
		runner.conn.Handle(svcvipconnect.VIPReadVIPProcedure, failWith(connect.NewError(connect.CodeNotFound, "not found")))

		_, err := runner.run("delete-vip", "--force", resourceUuid)

		Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
		Expect(runner.conn.Requests(svcvipconnect.VIPDeleteVIPProcedure)).To(BeEmpty())
	})

	It("is reachable through its alias", func() {
		runner.conn.Handle(svcvipconnect.VIPReadVIPProcedure, reply(&svcvip.VIPResponse{Vip: &vip.VIP{UUID: resourceUuid}}))

		_, err := runner.run("dvip", "--force", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(runner.conn.Requests(svcvipconnect.VIPDeleteVIPProcedure)).To(HaveLen(1))
	})
})
