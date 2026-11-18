package cli

import (
	"path/filepath"
	"strings"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
)

var _ = Describe("create-vip", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("sends the vip from the file without its uuid", func() {
		runner.conn.Handle(svcvipconnect.VIPCreateVIPProcedure, reply(&svcvip.VIPResponse{}))

		out, err := runner.run("create-vip", "--force", "--file", writeTempFile("vip.yml", "name: public\nip: 10.0.0.1\nUUID: "+resourceUuid))

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("VIP created successfully"))
		sent := lastRequest[*svcvip.CreateVIPRequest](runner.conn, svcvipconnect.VIPCreateVIPProcedure).GetVip()
		Expect(sent.GetName()).To(Equal("public"))
		Expect(sent.GetUUID()).To(BeEmpty())
	})

	It("writes a skeleton file with --init without calling the server", func() {
		path := filepath.Join(GinkgoT().TempDir(), "vip.yml")

		out, err := runner.run("create-vip", "--init", "--file", path)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Initialized json file"))
		Expect(path).To(BeAnExistingFile())
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("fails on an empty file", func() {
		_, err := runner.run("create-vip", "--force", "--file", writeTempFile("vip.yml", ""))

		Expect(err).To(MatchError(ContainSubstring("is empty")))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("prints the created vip as json with --json", func() {
		runner.conn.Handle(svcvipconnect.VIPCreateVIPProcedure, reply(&svcvip.VIPResponse{Vip: &vip.VIP{UUID: resourceUuid, Name: "public"}}))

		out, err := runner.run("create-vip", "--force", "--json", "--file", writeTempFile("vip.yml", "name: public"))

		Expect(err).NotTo(HaveOccurred())
		// The diff is printed first on the same output, the created vip comes last.
		jsonStart := strings.LastIndex(out, "\n{")
		Expect(jsonStart).To(BeNumerically(">", 0))
		Expect(parseJSON(out[jsonStart:])).To(HaveKeyWithValue("UUID", resourceUuid))
	})

	It("shows the change to be made before creating", func() {
		runner.conn.Handle(svcvipconnect.VIPCreateVIPProcedure, reply(&svcvip.VIPResponse{}))

		out, err := runner.run("create-vip", "--force", "--file", writeTempFile("vip.yml", "name: public\nip: 10.0.0.1"))

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Change to be made"))
		Expect(out).To(ContainSubstring("10.0.0.1"))
	})

	It("returns the server error", func() {
		runner.conn.Handle(svcvipconnect.VIPCreateVIPProcedure, failWith(connect.NewError(connect.CodePermissionDenied, "admin only")))

		_, err := runner.run("create-vip", "--force", "--file", writeTempFile("vip.yml", "name: public"))

		Expect(connect.CodeOf(err)).To(Equal(connect.CodePermissionDenied))
	})
})
