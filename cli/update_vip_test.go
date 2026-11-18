package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
)

var _ = Describe("update-vip", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcvipconnect.VIPReadVIPProcedure, reply(&svcvip.VIPResponse{Vip: &vip.VIP{UUID: resourceUuid, Name: "public"}}))
	})

	It("sends the file content with the uuid of the current vip", func() {
		runner.conn.Handle(svcvipconnect.VIPUpdateVIPProcedure, reply(&svcvip.VIPResponse{}))

		out, err := runner.run("update-vip", "--force", "--file", writeTempFile("vip.yml", "name: renamed"), resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("updated from file"))
		Expect(lastRequest[*svccore.UuidRequest](runner.conn, svcvipconnect.VIPReadVIPProcedure).GetUUID()).To(Equal(resourceUuid))
		sent := lastRequest[*svcvip.UpdateVIPRequest](runner.conn, svcvipconnect.VIPUpdateVIPProcedure)
		Expect(sent.GetUUID()).To(Equal(resourceUuid))
		Expect(sent.GetVip().GetName()).To(Equal("renamed"))
		Expect(sent.GetVip().GetUUID()).To(Equal(resourceUuid))
	})

	It("fails on an empty file without updating", func() {
		_, err := runner.run("update-vip", "--force", "--file", writeTempFile("vip.yml", ""), resourceUuid)

		Expect(err).To(MatchError(ContainSubstring("is empty")))
		Expect(runner.conn.Requests(svcvipconnect.VIPUpdateVIPProcedure)).To(BeEmpty())
	})

	It("finds the current vip by name", func() {
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, reply(&svcvip.ListVIPResponse{Vip: []*vip.VIP{{UUID: resourceUuid, Name: "public"}}}))
		runner.conn.Handle(svcvipconnect.VIPUpdateVIPProcedure, reply(&svcvip.VIPResponse{}))

		_, err := runner.run("update-vip", "--force", "--file", writeTempFile("vip.yml", "name: public\nip: 10.0.0.2"), "public")

		Expect(err).NotTo(HaveOccurred())
		Expect(lastRequest[*svcvip.UpdateVIPRequest](runner.conn, svcvipconnect.VIPUpdateVIPProcedure).GetUUID()).To(Equal(resourceUuid))
	})

	It("fails when no vip has the name", func() {
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, reply(&svcvip.ListVIPResponse{}))

		_, err := runner.run("update-vip", "--force", "--file", writeTempFile("vip.yml", "name: x"), "missing")

		Expect(err).To(MatchError(ContainSubstring("missing not found")))
	})
})
