package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
)

var _ = Describe("certificate", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svccertconnect.CertificateReadCertificateProcedure, reply(&svccert.CertificateResponse{Certificate: &cert.Certificate{UUID: resourceUuid, Name: "shown"}}))
	})

	It("prints the certificate configuration", func() {
		out, err := runner.run("certificate", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Certificate " + resourceUuid + " configuration"))
		Expect(out).To(ContainSubstring("name: shown"))
	})

	It("prints only the uuid with --uuid", func() {
		out, err := runner.run("certificate", "--uuid", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(resourceUuid + "\n"))
	})

	It("prints json with --json", func() {
		out, err := runner.run("certificate", "--json", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).To(HaveKeyWithValue("name", "shown"))
	})
})
