package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
)

var _ = Describe("show-linked-resource", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("shows the instances using a vip", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, firstPage(&svcinstance.ListInstanceResponse{
			Instances: []*instance.Instance{{UUID: "inst-1", Name: "lb"}},
		}))

		out, err := runner.run("show-linked-resource", "-r", "vip", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("instance"))
		Expect(out).To(ContainSubstring("inst-1"))
		Expect(out).To(ContainSubstring("lb"))
	})

	It("walks up from a backend to its virtual servers, vips and instances", func() {
		runner.conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{Vs: []*vs.VirtualServer{{UUID: "vs-1", Name: "web"}}}))
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{Vip: []*vip.VIP{{UUID: "vip-1", Name: "public"}}}))
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, firstPage(&svcinstance.ListInstanceResponse{
			Instances: []*instance.Instance{{UUID: "inst-1", Name: "lb"}},
		}))

		out, err := runner.run("slr", "-r", "backend", "--json", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		// Linked resources come from a map, so their order is random.
		Expect(parseJSON(out)).To(ConsistOf(
			map[string]any{"name": "web", "uuid": "vs-1", "type": "vs"},
			map[string]any{"name": "public", "uuid": "vip-1", "type": "vip"},
			map[string]any{"name": "lb", "uuid": "inst-1", "type": "instance"},
		))
	})

	It("tells how to find resources linked to an instance", func() {
		out, err := runner.run("show-linked-resource", "-r", "instance", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("simply read a single instance"))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("says when nothing is linked", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, firstPage(&svcinstance.ListInstanceResponse{}))

		out, err := runner.run("show-linked-resource", "-r", "vip", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("No linked resource found"))
	})
})
