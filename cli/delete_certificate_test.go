package cli

import (
	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("delete-certificate", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svccertconnect.CertificateDeleteCertificateProcedure, reply(&emptypb.Empty{}))
	})

	It("deletes the certificate found by uuid", func() {
		runner.conn.Handle(svccertconnect.CertificateReadCertificateProcedure, reply(&svccert.CertificateResponse{Certificate: &cert.Certificate{UUID: resourceUuid}}))

		out, err := runner.run("delete-certificate", "--force", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Certificate successfully deleted"))
		Expect(lastRequest[*svccore.UuidRequest](runner.conn, svccertconnect.CertificateDeleteCertificateProcedure).GetUUID()).To(Equal(resourceUuid))
	})

	It("does not delete when the certificate cannot be found", func() {
		runner.conn.Handle(svccertconnect.CertificateReadCertificateProcedure, failWith(connect.NewError(connect.CodeNotFound, "not found")))

		_, err := runner.run("delete-certificate", "--force", resourceUuid)

		Expect(connect.CodeOf(err)).To(Equal(connect.CodeNotFound))
		Expect(runner.conn.Requests(svccertconnect.CertificateDeleteCertificateProcedure)).To(BeEmpty())
	})
})
