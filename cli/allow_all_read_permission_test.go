package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	"github.com/seagalia/flock/sdk/flock/services/permission/v1/svcpermconnect"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("allow-all-read-permission", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcpermconnect.PermissionSetAllowAllReadProcedure, reply(&emptypb.Empty{}))
	})

	It("enables allow all read with --allow-all-read", func() {
		out, err := runner.run("allow-all-read-permission", "-r", "vip", "--allow-all-read", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Allow all read permission successfully set"))
		sent := lastRequest[*svcperm.SetAllowAllReadRequest](runner.conn, svcpermconnect.PermissionSetAllowAllReadProcedure)
		Expect(sent.GetAllowAllRead()).To(BeTrue())
		Expect(sent.GetResourceUuid()).To(Equal(resourceUuid))
	})

	It("disables allow all read without the flag", func() {
		_, err := runner.run("allow-all-read-permission", "-r", "vip", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		sent := lastRequest[*svcperm.SetAllowAllReadRequest](runner.conn, svcpermconnect.PermissionSetAllowAllReadProcedure)
		Expect(sent.GetAllowAllRead()).To(BeFalse())
	})
})
