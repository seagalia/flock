package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	"github.com/seagalia/flock/sdk/flock/services/permission/v1/svcpermconnect"
)

// The negative answer in human format calls os.Exit(1), so it cannot be tested.
var _ = Describe("has-permission", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("confirms when the member has the permission", func() {
		runner.conn.Handle(svcpermconnect.PermissionHasPermissionProcedure, reply(&svcperm.HasPermissionResponse{HasPermission: true}))

		out, err := runner.run("has-permission",
			"-r", "vip", "--role", "owner", "--member-type", "user", "--member-name", "alice", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Member alice has permission owner"))
		sent := lastRequest[*svcperm.HasPermissionRequest](runner.conn, svcpermconnect.PermissionHasPermissionProcedure)
		Expect(sent.GetRole()).To(Equal(permission.Role_OWNER))
	})

	It("prints the answer as json with --json, even when negative", func() {
		runner.conn.Handle(svcpermconnect.PermissionHasPermissionProcedure, reply(&svcperm.HasPermissionResponse{HasPermission: false}))

		out, err := runner.run("has-permission", "--json",
			"-r", "vip", "--member-type", "user", "--member-name", "alice", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).To(HaveKeyWithValue("has_permission", false))
	})
})
