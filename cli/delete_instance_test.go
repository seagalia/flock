package cli

import (
	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("delete-instance", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcinstanceconnect.InstanceDeleteInstanceProcedure, reply(&emptypb.Empty{}))
	})

	It("deletes the instance found by uuid", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceReadInstanceProcedure, reply(&svcinstance.InstanceResponse{Instance: &instance.Instance{UUID: resourceUuid}}))

		out, err := runner.run("delete-instance", "--force", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Instance successfully deleted"))
		Expect(lastRequest[*svccore.UuidRequest](runner.conn, svcinstanceconnect.InstanceDeleteInstanceProcedure).GetUUID()).To(Equal(resourceUuid))
	})

	It("does not delete when the instance cannot be found", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceReadInstanceProcedure, failWith(connect.NewError(connect.CodeNotFound, "not found")))

		_, err := runner.run("delete-instance", "--force", resourceUuid)

		Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
		Expect(runner.conn.Requests(svcinstanceconnect.InstanceDeleteInstanceProcedure)).To(BeEmpty())
	})
})
