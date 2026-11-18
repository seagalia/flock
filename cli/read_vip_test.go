package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
)

var _ = Describe("vip", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcvipconnect.VIPReadVIPProcedure, reply(&svcvip.VIPResponse{Vip: &vip.VIP{UUID: resourceUuid, Name: "shown"}}))
	})

	It("prints the vip configuration", func() {
		out, err := runner.run("vip", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Vip " + resourceUuid + " configuration"))
		Expect(out).To(ContainSubstring("name: shown"))
	})

	It("prints only the uuid with --uuid", func() {
		out, err := runner.run("vip", "--uuid", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(resourceUuid + "\n"))
	})

	It("prints json with --json", func() {
		out, err := runner.run("vip", "--json", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).To(HaveKeyWithValue("name", "shown"))
	})

	It("expands the vip with its virtual servers with --expand", func() {
		runner.conn.Handle(svcvipconnect.VIPReadVIPProcedure, reply(&svcvip.VIPResponse{Vip: &vip.VIP{
			UUID: resourceUuid, Name: "public", VirtualServersUuid: []string{"vs-1"},
		}}))
		runner.conn.Handle(svcvsconnect.VirtualServerReadVSProcedure, reply(&svcvs.VSResponse{Vs: &vs.VirtualServer{UUID: "vs-1", Name: "web"}}))

		out, err := runner.run("vip", "--expand", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("virtual_servers:"))
		Expect(out).To(ContainSubstring("name: web"))
	})
})
