package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	"github.com/seagalia/flock/sdk/flock/services/permission/v1/svcpermconnect"
)

var _ = Describe("permissions", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("lists every member permission when no role is given", func() {
		runner.conn.Handle(svcpermconnect.PermissionListMemberPermissionsProcedure, reply(&svcperm.ListMemberPermissionsResponse{
			MemberPermissions: []*permission.MemberPermission{
				{Role: permission.Role_OWNER, Member: &permission.Member{MemberType: permission.MemberType_GROUP, MemberName: "network"}},
			},
		}))

		out, err := runner.run("permissions", "-r", "vip", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("GROUP"))
		Expect(out).To(ContainSubstring("network"))
		Expect(out).To(ContainSubstring("OWNER"))
		sent := lastRequest[*svcperm.ListMemberPermissionsRequest](runner.conn, svcpermconnect.PermissionListMemberPermissionsProcedure)
		Expect(sent.GetSeeAll()).To(BeTrue())
	})

	It("filters on the given role", func() {
		runner.conn.Handle(svcpermconnect.PermissionListMemberPermissionsProcedure, reply(&svcperm.ListMemberPermissionsResponse{}))

		out, err := runner.run("p", "-r", "vip", "--role", "writer", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("No permissions found."))
		sent := lastRequest[*svcperm.ListMemberPermissionsRequest](runner.conn, svcpermconnect.PermissionListMemberPermissionsProcedure)
		Expect(sent.GetSeeAll()).To(BeFalse())
		Expect(sent.GetRole()).To(Equal(permission.Role_WRITER))
	})
})
