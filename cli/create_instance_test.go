package cli

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
)

var _ = Describe("create-instance", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("sends the instance from the file without its uuid", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceCreateInstanceProcedure, reply(&svcinstance.InstanceResponse{}))

		out, err := runner.run("create-instance", "--force", "--file", writeTempFile("instance.yml", "name: lb\nUUID: "+resourceUuid))

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Instance created successfully"))
		sent := lastRequest[*svcinstance.CreateInstanceRequest](runner.conn, svcinstanceconnect.InstanceCreateInstanceProcedure).GetInstance()
		Expect(sent.GetName()).To(Equal("lb"))
		Expect(sent.GetUUID()).To(BeEmpty())
	})

	It("writes a skeleton file with --init without calling the server", func() {
		path := filepath.Join(GinkgoT().TempDir(), "instance.yml")

		out, err := runner.run("create-instance", "--init", "--file", path)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Initialized json file"))
		Expect(path).To(BeAnExistingFile())
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("fails on an empty file", func() {
		_, err := runner.run("create-instance", "--force", "--file", writeTempFile("instance.yml", ""))

		Expect(err).To(MatchError(ContainSubstring("is empty")))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})
})
