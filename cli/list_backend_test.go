package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("backends", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("prints every backend with its policy, health check and endpoints", func() {
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure, firstPage(&svcbackend.ListBackendResponse{
			Backends: []*backend.Backend{{Name: "backend", LbPolicy: backend.Backend_MAGLEV}},
		}))

		out, err := runner.run("backends")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("backend"))
		Expect(out).To(ContainSubstring("MAGLEV"))
		Expect(out).To(ContainSubstring("HTTP"))
		Expect(out).To(ContainSubstring("Unknown"))
	})

	It("says when there is no backend", func() {
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure, firstPage(&svcbackend.ListBackendResponse{}))

		out, err := runner.run("c")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("No backends found."))
	})

	It("sends metadata filters given with --filter", func() {
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure, firstPage(&svcbackend.ListBackendResponse{}))

		_, err := runner.run("backends", "--filter", "env!=prod")

		Expect(err).NotTo(HaveOccurred())
		Expect(sentFilters(runner, svcbackendconnect.BackendListBackendProcedure)).To(ConsistOf(HaveField("Key", "env")))
	})

	It("lists the backends of a virtual server found by name with --vs", func() {
		runner.conn.Handle(svcvsconnect.VirtualServerListVSProcedure, reply(&svcvs.ListVSResponse{Vs: []*vs.VirtualServer{{UUID: resourceUuid}}}))
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure, firstPage(&svcbackend.ListBackendResponse{}))

		_, err := runner.run("backends", "--vs", "web")

		Expect(err).NotTo(HaveOccurred())
		filters := sentFilters(runner, svcbackendconnect.BackendListBackendProcedure)
		Expect(filters).To(ContainElement(HaveField("Key", helpers.TypeUrl[*vs.VirtualServer]())))
		Expect(filters).To(ContainElement(HaveField("Value", resourceUuid)))
	})

	It("shows the parent virtual servers with --show-parent", func() {
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure, firstPage(&svcbackend.ListBackendResponse{
			Backends: []*backend.Backend{{UUID: "backend-1", Name: "backend"}},
		}))
		runner.conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{Vs: []*vs.VirtualServer{{Name: "web"}}}))

		out, err := runner.run("backends", "--show-parent")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("web"))
		Expect(sentFilters(runner, svcvsconnect.VirtualServerListVSProcedure)[0].GetValue()).To(Equal("backend-1"))
	})

	DescribeTable("CropUuid",
		func(uuid, want string) {
			Expect(CropUuid(uuid)).To(Equal(want))
		},
		Entry("long uuid is cropped", "0f8fad5b-d9cb-469f-a165-70867728950e", "0f8fad5b"),
		Entry("short value is kept", "abc", "abc"),
	)

	DescribeTable("getHealthCheckType",
		func(healthCheck *core.HealthCheck, want string) {
			Expect(getHealthCheckType(&backend.Backend{HealthCheck: healthCheck})).To(Equal(want))
		},
		Entry("defaults to http", nil, "HTTP"),
		Entry("grpc", &core.HealthCheck{HealthChecker: &core.HealthCheck_GrpcHealthCheck{GrpcHealthCheck: &core.GrpcHealthCheck{}}}, "GRPC"),
		Entry("tcp", &core.HealthCheck{HealthChecker: &core.HealthCheck_TcpHealthCheck{TcpHealthCheck: &core.TcpHealthCheck{}}}, "TCP"),
		Entry("none", &core.HealthCheck{HealthChecker: &core.HealthCheck_NoHealthCheck{NoHealthCheck: &core.NoHealthCheck{}}}, "NO"),
	)

	DescribeTable("getEndpointConfig",
		func(backendInfo *backend.Backend, want string) {
			Expect(getEndpointConfig(backendInfo)).To(Equal(want))
		},
		Entry("static ips",
			&backend.Backend{Endpoint: &backend.Backend_IpAddrGroup{IpAddrGroup: &core.IPAddrGroup{IpAddresses: []*core.IPAddr{
				{Ip: "10.0.0.1", Port: 80},
				{Ip: "::1", Port: 443},
			}}}},
			"Static: 10.0.0.1:80 [::1]:443"),
		Entry("eds",
			&backend.Backend{Endpoint: &backend.Backend_Eds{Eds: &backend.EDS{Url: &core.URL{Scheme: "https", Host: "eds.local", Port: 8443, Path: "/v1"}}}},
			"EDS: https://eds.local:8443/v1"),
		Entry("dns without port",
			&backend.Backend{Endpoint: &backend.Backend_Dns{Dns: &backend.DNS{Host: "backend.local", LookupFamily: backend.DNS_V4_ONLY}}},
			"DNS lookup V4_ONLY for host backend.local"),
		Entry("dns with port and nameservers",
			&backend.Backend{Endpoint: &backend.Backend_Dns{Dns: &backend.DNS{
				Host:        "backend.local",
				Port:        8080,
				Nameservers: []*core.IPAddr{{Ip: "1.1.1.1", Port: 53}, {Ip: "8.8.8.8", Port: 53}},
			}}},
			"DNS lookup V6_PREFERRED for host backend.local port 8080\nnameservers 1.1.1.1:53,\n 8.8.8.8:53"),
		Entry("unknown", &backend.Backend{}, "Unknown"),
	)
})
