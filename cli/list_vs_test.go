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
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("virtual-servers", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("prints every virtual server with its port and route type", func() {
		runner.conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{Vs: []*vs.VirtualServer{
			{UUID: resourceUuid, Name: "web", Port: 443, RouteConfig: &vs.VirtualServer_TcpRouteConfig{TcpRouteConfig: &vs.TcpRouteConfig{}}},
		}}))

		out, err := runner.run("virtual-servers", "--show-uuid")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring(resourceUuid))
		Expect(out).To(ContainSubstring("web"))
		Expect(out).To(ContainSubstring("443"))
		Expect(out).To(ContainSubstring("TCP"))
	})

	It("is reachable through its aliases and says when there is no virtual server", func() {
		runner.conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{}))

		for _, alias := range []string{"list-vs", "v", "vss"} {
			out, err := runner.run(alias)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("No virtual servers found."))
		}
	})

	It("sends metadata filters given with --filter", func() {
		runner.conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{}))

		_, err := runner.run("virtual-servers", "--filter", "env==prod")

		Expect(err).NotTo(HaveOccurred())
		Expect(sentFilters(runner, svcvsconnect.VirtualServerListVSProcedure)).To(ConsistOf(HaveField("Key", "env")))
	})

	It("lists the virtual servers of a vip found by name with --vip", func() {
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, reply(&svcvip.ListVIPResponse{Vip: []*vip.VIP{{UUID: resourceUuid}}}))
		runner.conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{}))

		_, err := runner.run("virtual-servers", "--vip", "public")

		Expect(err).NotTo(HaveOccurred())
		filters := sentFilters(runner, svcvsconnect.VirtualServerListVSProcedure)
		Expect(filters).To(ContainElement(HaveField("Key", helpers.TypeUrl[*vip.VIP]())))
		Expect(filters).To(ContainElement(HaveField("Value", resourceUuid)))
	})

	DescribeTable("getRouteType",
		func(virtServ *vs.VirtualServer, want string) {
			Expect(getRouteType(virtServ)).To(Equal(want))
		},
		Entry("tcp", &vs.VirtualServer{RouteConfig: &vs.VirtualServer_TcpRouteConfig{TcpRouteConfig: &vs.TcpRouteConfig{}}}, "TCP"),
		Entry("udp", &vs.VirtualServer{RouteConfig: &vs.VirtualServer_UdpRouteConfig{UdpRouteConfig: &vs.UdpRouteConfig{}}}, "UDP"),
		Entry("http", &vs.VirtualServer{RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{}}}, "HTTP"),
		Entry("tls", &vs.VirtualServer{RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{}}}, "TLS over TCP"),
		Entry("none", &vs.VirtualServer{}, "UNKNOWN"),
	)
})
