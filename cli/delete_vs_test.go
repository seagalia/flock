package cli

import (
	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("delete-vs", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcvsconnect.VirtualServerDeleteVSProcedure, reply(&emptypb.Empty{}))
	})

	It("deletes the virtual server found by uuid", func() {
		runner.conn.Handle(svcvsconnect.VirtualServerReadVSProcedure, reply(&svcvs.VSResponse{Vs: &vs.VirtualServer{UUID: resourceUuid}}))

		out, err := runner.run("delete-vs", "--force", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Virtual Server successfully deleted"))
		Expect(lastRequest[*svccore.UuidRequest](runner.conn, svcvsconnect.VirtualServerDeleteVSProcedure).GetUUID()).To(Equal(resourceUuid))
	})

	It("does not delete when the virtual server cannot be found", func() {
		runner.conn.Handle(svcvsconnect.VirtualServerReadVSProcedure, failWith(connect.NewError(connect.CodeNotFound, "not found")))

		_, err := runner.run("delete-vs", "--force", resourceUuid)

		Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
		Expect(runner.conn.Requests(svcvsconnect.VirtualServerDeleteVSProcedure)).To(BeEmpty())
	})
})
