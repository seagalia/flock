package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
)

var _ = Describe("update-certificate", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svccertconnect.CertificateReadCertificateProcedure, reply(&svccert.CertificateResponse{
			Certificate: &cert.Certificate{UUID: resourceUuid, Name: "web", CertChain: "OLD"},
		}))
		runner.conn.Handle(svccertconnect.CertificateUpdateCertificateProcedure, reply(&svccert.CertificateResponse{}))
	})

	It("sends the new files and keeps the current name", func() {
		out, err := runner.run("update-certificate", "--force",
			"--cert", writeTempFile("cert.pem", "CERT"), "--key", writeTempFile("key.pem", "KEY"), resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Certificate updated successfully"))
		sent := lastRequest[*svccert.UpdateCertificateRequest](runner.conn, svccertconnect.CertificateUpdateCertificateProcedure)
		Expect(sent.GetUUID()).To(Equal(resourceUuid))
		Expect(sent.GetCertificate().GetName()).To(Equal("web"))
		Expect(sent.GetCertificate().GetCertChain()).To(Equal("CERT"))
		Expect(sent.GetCertificate().GetPrivateKey()).To(Equal("KEY"))
	})

	It("renames the certificate with --name", func() {
		_, err := runner.run("update-certificate", "--force", "--name", "renamed",
			"--cert", writeTempFile("cert.pem", "CERT"), "--key", writeTempFile("key.pem", "KEY"), resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		sent := lastRequest[*svccert.UpdateCertificateRequest](runner.conn, svccertconnect.CertificateUpdateCertificateProcedure)
		Expect(sent.GetCertificate().GetName()).To(Equal("renamed"))
	})
})
