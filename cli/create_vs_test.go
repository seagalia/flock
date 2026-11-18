package cli

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
)

var _ = Describe("create-vs", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("sends the virtual server from the file without its uuid", func() {
		runner.conn.Handle(svcvsconnect.VirtualServerCreateVSProcedure, reply(&svcvs.VSResponse{}))

		out, err := runner.run("create-vs", "--force", "--file", writeTempFile("vs.yml", "name: web\nport: 443\nUUID: "+resourceUuid))

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Virtual Server created successfully"))
		sent := lastRequest[*svcvs.CreateVSRequest](runner.conn, svcvsconnect.VirtualServerCreateVSProcedure).GetVs()
		Expect(sent.GetName()).To(Equal("web"))
		Expect(sent.GetUUID()).To(BeEmpty())
	})

	It("writes a skeleton file with --init without calling the server", func() {
		path := filepath.Join(GinkgoT().TempDir(), "vs.yml")

		out, err := runner.run("create-vs", "--init", "--file", path)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Initialized json file"))
		Expect(path).To(BeAnExistingFile())
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("fails on an empty file", func() {
		_, err := runner.run("create-vs", "--force", "--file", writeTempFile("vs.yml", ""))

		Expect(err).To(MatchError(ContainSubstring("is empty")))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})
})
