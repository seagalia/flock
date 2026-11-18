package cli

import (
	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("delete-backend", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcbackendconnect.BackendDeleteBackendProcedure, reply(&emptypb.Empty{}))
	})

	It("deletes the backend found by uuid", func() {
		runner.conn.Handle(svcbackendconnect.BackendReadBackendProcedure, reply(&svcbackend.BackendResponse{Backend: &backend.Backend{UUID: resourceUuid}}))

		out, err := runner.run("delete-backend", "--force", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Backend successfully deleted"))
		Expect(lastRequest[*svccore.UuidRequest](runner.conn, svcbackendconnect.BackendDeleteBackendProcedure).GetUUID()).To(Equal(resourceUuid))
	})

	It("does not delete when the backend cannot be found", func() {
		runner.conn.Handle(svcbackendconnect.BackendReadBackendProcedure, failWith(connect.NewError(connect.CodeNotFound, "not found")))

		_, err := runner.run("delete-backend", "--force", resourceUuid)

		Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
		Expect(runner.conn.Requests(svcbackendconnect.BackendDeleteBackendProcedure)).To(BeEmpty())
	})
})
