package cli

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
)

var _ = Describe("create-certificate", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("sends the content of the certificate, key and ca files", func() {
		runner.conn.Handle(svccertconnect.CertificateCreateCertificateProcedure, reply(&svccert.CertificateResponse{}))

		out, err := runner.run("create-certificate",
			"--name", "web",
			"--cert", writeTempFile("cert.pem", "CERT"),
			"--key", writeTempFile("key.pem", "PRIVATE-KEY-PEM"),
			"--ca", writeTempFile("ca.pem", "CA"),
			"--domains", "example.com", "--domains", "www.example.com", "--force")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Certificate created successfully"))
		Expect(out).NotTo(ContainSubstring("PRIVATE-KEY-PEM"))
		Expect(out).To(ContainSubstring("<hidden>"))
		sent := lastRequest[*svccert.CreateCertificateRequest](runner.conn, svccertconnect.CertificateCreateCertificateProcedure).GetCertificate()
		Expect(sent.GetName()).To(Equal("web"))
		Expect(sent.GetCertChain()).To(Equal("CERT"))
		Expect(sent.GetPrivateKey()).To(Equal("PRIVATE-KEY-PEM"))
		Expect(sent.GetCa()).To(Equal("CA"))
		Expect(sent.GetDomainNames()).To(Equal([]string{"example.com", "www.example.com"}))
	})

	It("fails on a missing key file without calling the server", func() {
		_, err := runner.run("create-certificate",
			"--name", "web",
			"--cert", writeTempFile("cert.pem", "CERT"),
			"--key", filepath.Join(GinkgoT().TempDir(), "missing.pem"))

		Expect(err).To(MatchError(ContainSubstring("error when reading key file")))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("sends the certificate of the file with its metadata, the flags overriding it", func() {
		runner.conn.Handle(svccertconnect.CertificateCreateCertificateProcedure, reply(&svccert.CertificateResponse{}))
		file := writeTempFile("cert.yml", `name: from-file
domain_names: [example.com]
metadata:
  flock.provisioner.acme: "true"
  flock.acme.email: ops@example.com
`)

		_, err := runner.run("create-certificate", "-f", file, "--domains", "www.example.com", "--force")

		Expect(err).NotTo(HaveOccurred())
		sent := lastRequest[*svccert.CreateCertificateRequest](runner.conn, svccertconnect.CertificateCreateCertificateProcedure).GetCertificate()
		Expect(sent.GetName()).To(Equal("from-file"))
		Expect(sent.GetDomainNames()).To(Equal([]string{"www.example.com"}))
		Expect(sent.GetMetadata()).To(HaveKeyWithValue("flock.provisioner.acme", "true"))
		Expect(sent.GetMetadata()).To(HaveKeyWithValue("flock.acme.email", "ops@example.com"))
		Expect(sent.GetPrivateKey()).To(BeEmpty())
	})

	It("refuses a certificate without a name without calling the server", func() {
		_, err := runner.run("create-certificate", "-f", writeTempFile("cert.yml", "domain_names: [example.com]\n"))

		Expect(err).To(MatchError(ContainSubstring("needs a name")))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("refuses a file adding the provisioner lock without calling the server", func() {
		file := writeTempFile("cert.yml", "name: web\nmetadata:\n  flock.autoprovision: \"true\"\n")

		_, err := runner.run("create-certificate", "-f", file)

		Expect(err).To(MatchError(ContainSubstring("reserved to automatic provisioning")))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})
})
