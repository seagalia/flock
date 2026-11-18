package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	"github.com/seagalia/flock/sdk/flock/services/permission/v1/svcpermconnect"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("delete-permission", func() {
	It("removes the member from the resource", func() {
		runner := newCommandRunner()
		runner.conn.Handle(svcpermconnect.PermissionDeletePermissionProcedure, reply(&emptypb.Empty{}))

		out, err := runner.run("delete-permission",
			"-r", "vip", "--member-type", "user", "--member-name", "alice", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Permission successfully deleted."))
		sent := lastRequest[*svcperm.DeletePermissionRequest](runner.conn, svcpermconnect.PermissionDeletePermissionProcedure)
		Expect(sent.GetResourceUuid()).To(Equal(resourceUuid))
		Expect(sent.GetMember().GetMemberName()).To(Equal("alice"))
		Expect(sent.GetMember().GetMemberType()).To(Equal(permission.MemberType_USER))
	})
})
