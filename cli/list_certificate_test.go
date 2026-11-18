package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("certificates", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("prints every certificate with its domains", func() {
		runner.conn.Handle(svccertconnect.CertificateListCertificateProcedure, firstPage(&svccert.ListCertificateResponse{
			Certificates: []*cert.Certificate{{Name: "web-cert", DomainNames: []string{"a.example.com", "b.example.com"}}},
		}))

		out, err := runner.run("certificates")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("web-cert"))
		Expect(out).To(ContainSubstring("a.example.com, b.example.com"))
	})

	It("says when there is no certificate", func() {
		runner.conn.Handle(svccertconnect.CertificateListCertificateProcedure, firstPage(&svccert.ListCertificateResponse{}))

		out, err := runner.run("certs")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("No certificates found."))
	})

	It("sends metadata filters given with --filter", func() {
		runner.conn.Handle(svccertconnect.CertificateListCertificateProcedure, firstPage(&svccert.ListCertificateResponse{}))

		_, err := runner.run("certificates", "--filter", "env==prod")

		Expect(err).NotTo(HaveOccurred())
		Expect(sentFilters(runner, svccertconnect.CertificateListCertificateProcedure)).To(ConsistOf(HaveField("Key", "env")))
	})

	It("lists the certificates of a virtual server found by name with --vs", func() {
		runner.conn.Handle(svcvsconnect.VirtualServerListVSProcedure, reply(&svcvs.ListVSResponse{Vs: []*vs.VirtualServer{{UUID: resourceUuid}}}))
		runner.conn.Handle(svccertconnect.CertificateListCertificateProcedure, firstPage(&svccert.ListCertificateResponse{}))

		_, err := runner.run("certificates", "--vs", "web")

		Expect(err).NotTo(HaveOccurred())
		filters := sentFilters(runner, svccertconnect.CertificateListCertificateProcedure)
		Expect(filters).To(ContainElement(HaveField("Key", helpers.TypeUrl[*vs.VirtualServer]())))
		Expect(filters).To(ContainElement(HaveField("Value", resourceUuid)))
	})
})
