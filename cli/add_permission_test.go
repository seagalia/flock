package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	"github.com/seagalia/flock/sdk/flock/services/permission/v1/svcpermconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("add-permission", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcpermconnect.PermissionAddPermissionProcedure, reply(&emptypb.Empty{}))
	})

	It("adds the role for the member on the resource uuid", func() {
		out, err := runner.run("add-permission",
			"-r", "vip", "--role", "writer", "--member-type", "group", "--member-name", "network", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Permission WRITER added"))
		sent := lastRequest[*svcperm.AddPermissionRequest](runner.conn, svcpermconnect.PermissionAddPermissionProcedure)
		Expect(sent.GetResourceType()).To(Equal(helpers.TypeUrl[*vip.VIP]()))
		Expect(sent.GetResourceUuid()).To(Equal(resourceUuid))
		Expect(sent.GetRole()).To(Equal(permission.Role_WRITER))
		Expect(sent.GetMember().GetMemberType()).To(Equal(permission.MemberType_GROUP))
		Expect(sent.GetMember().GetMemberName()).To(Equal("network"))
	})

	It("resolves a resource name to its uuid and defaults to the reader role", func() {
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure, reply(&svcbackend.ListBackendResponse{
			Backends: []*backend.Backend{{UUID: resourceUuid, Name: "backend"}},
		}))

		_, err := runner.run("add-permission",
			"-r", "backend", "--member-type", "user", "--member-name", "alice", "backend")

		Expect(err).NotTo(HaveOccurred())
		sent := lastRequest[*svcperm.AddPermissionRequest](runner.conn, svcpermconnect.PermissionAddPermissionProcedure)
		Expect(sent.GetResourceUuid()).To(Equal(resourceUuid))
		Expect(sent.GetRole()).To(Equal(permission.Role_READER))
	})

	It("gives a role on a secret", func() {
		_, err := runner.run("add-permission",
			"-r", "secret", "--member-type", "user", "--member-name", "alice", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		sent := lastRequest[*svcperm.AddPermissionRequest](runner.conn, svcpermconnect.PermissionAddPermissionProcedure)
		Expect(sent.GetResourceType()).To(Equal(helpers.TypeUrl[*secret.Secret]()))
	})

	It("rejects an unknown resource type", func() {
		_, err := runner.run("add-permission",
			"-r", "route", "--member-type", "user", "--member-name", "alice", resourceUuid)

		Expect(err).To(MatchError(And(ContainSubstring("must be one of"), ContainSubstring(`"route"`))))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})
})
