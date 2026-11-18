package as3_test

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"google.golang.org/protobuf/types/known/durationpb"
)

var _ = Describe("Render", func() {
	It("renders a tcp virtual server as a Service_TCP in the application of its VIP", func() {
		v := newVIP(vipUUID, "10.99.0.1", vsUUID)
		res := as3.Render(input(v, tcpVS(vsUUID, 443, backendUUID), staticBackend(backendUUID)))

		app := res.Tenant.Apps["vip_11111111_1111_1111_1111_111111111111"]
		Expect(app).ToNot(BeNil())
		Expect(app.Remark).To(Equal(v.GetName()))
		svc := service(res, vipUUID, vsUUID)
		Expect(svc.Class).To(Equal("Service_TCP"))
		Expect(svc.VirtualAddresses).To(Equal([]string{"10.99.0.1"}))
		Expect(svc.VirtualPort).To(BeEquivalentTo(443))
		Expect(svc.Pool).To(Equal("pool_33333333_3333_3333_3333_333333333333"))
		Expect(svc.Snat).To(Equal("auto"))
		Expect(svc.PersistenceMethods).ToNot(BeNil())
		Expect(svc.PersistenceMethods).To(BeEmpty())
		Expect(res.Errors).To(BeEmpty())
		Expect(res.Missing).To(BeEmpty())
	})

	It("renders a udp virtual server as a Service_UDP", func() {
		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), udpVS(vsUUID, 53, backendUUID), staticBackend(backendUUID)))

		Expect(service(res, vipUUID, vsUUID).Class).To(Equal("Service_UDP"))
	})

	It("writes an explicit empty persistence list in the json", func() {
		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), staticBackend(backendUUID)))

		b, err := json.Marshal(service(res, vipUUID, vsUUID))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"persistenceMethods":[]`))
	})

	It("renders the addresses of a static backend as pool members with their weight as ratio", func() {
		b := staticBackend(backendUUID)
		b.GetIpAddrGroup().IpAddresses = []*core.IPAddr{
			{Ip: "10.0.0.11", Port: 8080},
			{Ip: "10.0.0.10", Port: 8080},
			{Ip: "10.0.0.12", Port: 9090, LoadBalancingWeight: 3},
		}

		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), b))

		p := pool(res, vipUUID, backendUUID)
		Expect(p.Members).To(Equal([]as3.Member{
			{ServicePort: 8080, ServerAddresses: []string{"10.0.0.10", "10.0.0.11"}, ShareNodes: true},
			{ServicePort: 9090, ServerAddresses: []string{"10.0.0.12"}, Ratio: 3, ShareNodes: true},
		}), "shared nodes: BIG-IP refuses one address as a node of two partitions")
		Expect(p.Remark).To(Equal(b.GetName()))
		Expect(res.Missing).To(BeEmpty())
	})

	It("renders the endpoints the agent discovered for an EDS backend", func() {
		in := input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), edsBackend(backendUUID))
		in.Endpoints[backendUUID] = []*core.IPAddr{{Ip: "10.0.0.20", Port: 8080}}

		res := as3.Render(in)

		Expect(pool(res, vipUUID, backendUUID).Members).To(Equal([]as3.Member{
			{ServicePort: 8080, ServerAddresses: []string{"10.0.0.20"}, ShareNodes: true},
		}))
		Expect(res.Missing).To(BeEmpty())
	})

	It("lets the BIG-IP resolve the name of a DNS backend", func() {
		b := dnsBackend(backendUUID, "app.example.com", backend.DNS_V4_ONLY)

		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), b))

		Expect(pool(res, vipUUID, backendUUID).Members).To(Equal([]as3.Member{{
			ServicePort:      8080,
			AddressDiscovery: "fqdn",
			Hostname:         "app.example.com",
			AutoPopulate:     true,
			AddressFamily:    "IPv4",
			QueryInterval:    5,
			ShareNodes:       true,
		}}))
		Expect(res.Missing).To(BeEmpty())
		Expect(res.Warnings[backendKey(backendUUID)]).To(BeEmpty())
	})

	It("queries the DNS at least every second", func() {
		b := dnsBackend(backendUUID, "app.example.com", backend.DNS_V4_ONLY)
		b.RefreshRate = durationpb.New(1500 * time.Millisecond)

		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), b))

		Expect(pool(res, vipUUID, backendUUID).Members[0].QueryInterval).To(BeEquivalentTo(2))
	})

	DescribeTable("maps the lookup family to the one address family a BIG-IP FQDN member queries",
		func(family backend.DNS_LookupFamily, addressFamily string, warning string) {
			b := dnsBackend(backendUUID, "app.example.com", family)

			res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), b))

			Expect(pool(res, vipUUID, backendUUID).Members[0].AddressFamily).To(Equal(addressFamily))
			if warning == "" {
				Expect(res.Warnings[backendKey(backendUUID)]).To(BeEmpty())
				return
			}
			Expect(res.Warnings[backendKey(backendUUID)]).To(ConsistOf(warning))
		},
		Entry("v4 only", backend.DNS_V4_ONLY, "IPv4", ""),
		Entry("v6 only", backend.DNS_V6_ONLY, "IPv6", ""),
		Entry("v4 preferred", backend.DNS_V4_PREFERRED, "IPv4",
			"lookup_family V4_PREFERRED has no F5 equivalent, the BIG-IP queries A records only"),
		Entry("v6 preferred", backend.DNS_V6_PREFERRED, "IPv4",
			"lookup_family V6_PREFERRED has no F5 equivalent, the BIG-IP queries A records only"),
		Entry("all", backend.DNS_ALL, "IPv4",
			"lookup_family ALL has no F5 equivalent, the BIG-IP queries A records only"),
	)

	It("renders the endpoints the agent resolved for a DNS backend with its own nameservers", func() {
		b := dnsBackend(backendUUID, "app.example.com", backend.DNS_V6_PREFERRED)
		b.GetDns().Nameservers = []*core.IPAddr{{Ip: "10.0.0.53", Port: 53}}
		in := input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), b)
		in.Endpoints[backendUUID] = []*core.IPAddr{{Ip: "10.0.0.30", Port: 8080}}

		res := as3.Render(in)

		Expect(pool(res, vipUUID, backendUUID).Members).To(Equal([]as3.Member{
			{ServicePort: 8080, ServerAddresses: []string{"10.0.0.30"}, ShareNodes: true},
		}))
		Expect(res.Warnings[backendKey(backendUUID)]).To(BeEmpty())
	})

	DescribeTable("tells which backends the agent discovers itself",
		func(b *backend.Backend, agentDiscovers bool) {
			Expect(as3.AgentDiscovers(b)).To(Equal(agentDiscovers))
		},
		Entry("static", staticBackend(backendUUID), false),
		Entry("dns", dnsBackend(backendUUID, "app.example.com", backend.DNS_V4_ONLY), false),
		Entry("dns with nameservers", func() *backend.Backend {
			b := dnsBackend(backendUUID, "app.example.com", backend.DNS_V4_ONLY)
			b.GetDns().Nameservers = []*core.IPAddr{{Ip: "10.0.0.53", Port: 53}}
			return b
		}(), true),
		Entry("eds", edsBackend(backendUUID), true),
	)

	DescribeTable("maps lb_policy to an AS3 load balancing mode",
		func(policy backend.Backend_LbPolicy, mode string) {
			b := staticBackend(backendUUID)
			b.LbPolicy = policy
			res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), b))

			Expect(pool(res, vipUUID, backendUUID).LoadBalancingMode).To(Equal(mode))
			Expect(res.Warnings[backendKey(backendUUID)]).To(BeEmpty())
		},
		Entry("round robin", backend.Backend_ROUND_ROBIN, "round-robin"),
		Entry("weighted round robin", backend.Backend_WEIGHTED_ROUND_ROBIN, "ratio-member"),
		Entry("least request", backend.Backend_LEAST_REQUEST, "least-connections-member"),
		Entry("weighted least request", backend.Backend_WEIGHTED_LEAST_REQUEST, "ratio-least-connections-member"),
	)

	DescribeTable("approximates consistent hashing with source address persistence",
		func(policy backend.Backend_LbPolicy, name string) {
			b := staticBackend(backendUUID)
			b.LbPolicy = policy
			res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), b))

			Expect(pool(res, vipUUID, backendUUID).LoadBalancingMode).To(Equal("least-connections-member"))
			Expect(service(res, vipUUID, vsUUID).PersistenceMethods).To(Equal([]any{"source-address"}))
			Expect(res.Warnings[backendKey(backendUUID)]).To(ContainElement(
				"lb_policy " + name + " has no F5 equivalent, least-connections-member with source-address persistence is used"))
		},
		Entry("ring hash", backend.Backend_RING_HASH, "RING_HASH"),
		Entry("maglev", backend.Backend_MAGLEV, "MAGLEV"),
	)

	It("leaves out a virtual server whose backend is not known and reports it missing", func() {
		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID)))

		Expect(res.Tenant.Apps[as3.AppName(vipUUID)].Objects).ToNot(HaveKey(as3.ServiceName(vsUUID)))
		Expect(res.Missing[vsKey(vsUUID)]).To(ConsistOf(backendKey(backendUUID)))
	})

	It("reports a virtual server of a VIP that is not known as missing", func() {
		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID)))

		Expect(res.Missing[vipKey(vipUUID)]).To(ConsistOf(vsKey(vsUUID)))
		Expect(res.Tenant.Apps).To(HaveKey(as3.AppName(vipUUID)))
	})

	It("renders a backend not discovered yet with no member and reports it missing", func() {
		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), edsBackend(backendUUID)))

		Expect(pool(res, vipUUID, backendUUID).Members).To(BeEmpty())
		Expect(res.Missing[vsKey(vsUUID)]).To(ConsistOf(backendKey(backendUUID)))
	})

	It("refuses a second virtual server on the same tcp port of a VIP", func() {
		otherUUID := "99999999-9999-9999-9999-999999999999"
		first := tcpVS(vsUUID, 443, backendUUID)
		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID, otherUUID), first,
			tcpVS(otherUUID, 443, backendUUID), staticBackend(backendUUID)))

		Expect(service(res, vipUUID, vsUUID)).ToNot(BeNil())
		Expect(res.Tenant.Apps[as3.AppName(vipUUID)].Objects).ToNot(HaveKey(as3.ServiceName(otherUUID)))
		Expect(res.Errors[vsKey(otherUUID)]).To(Equal(
			"port 443 of the VIP is already used by virtual server " + first.GetName() + ", the F5 agent can't share it"))
	})

	It("renders tcp and udp virtual servers on the same port", func() {
		otherUUID := "99999999-9999-9999-9999-999999999999"
		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID, otherUUID), tcpVS(vsUUID, 53, backendUUID),
			udpVS(otherUUID, 53, backendUUID), staticBackend(backendUUID)))

		Expect(service(res, vipUUID, vsUUID).Class).To(Equal("Service_TCP"))
		Expect(service(res, vipUUID, otherUUID).Class).To(Equal("Service_UDP"))
		Expect(res.Errors).To(BeEmpty())
	})

	It("lists the pools of each backend", func() {
		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), staticBackend(backendUUID)))

		Expect(res.Pools).To(ConsistOf(as3.PoolRef{
			BackendUUID: backendUUID, Tenant: "flock", App: as3.AppName(vipUUID), Pool: as3.PoolName(backendUUID),
		}))
	})

	It("renders deterministically", func() {
		otherUUID := "99999999-9999-9999-9999-999999999999"
		in := input(newVIP(vipUUID, "10.99.0.1", vsUUID, otherUUID), tcpVS(vsUUID, 443, backendUUID),
			udpVS(otherUUID, 53, backendUUID), staticBackend(backendUUID))

		first, err := as3.Document("inst-1", "flock", as3.Render(in).Tenant, false)
		Expect(err).ToNot(HaveOccurred())
		for range 20 {
			again, err := as3.Document("inst-1", "flock", as3.Render(in).Tenant, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(again).To(Equal(first))
		}
	})

	It("keeps object names under the AS3 length limit", func() {
		Expect(len(as3.AppName(vipUUID))).To(BeNumerically("<=", 64))
		Expect(len(as3.ServiceName(vsUUID))).To(BeNumerically("<=", 64))
		Expect(len(as3.IRuleName("proxyrecv", vsUUID))).To(BeNumerically("<=", 64))
	})
})

var _ = Describe("Document", func() {
	It("wraps the tenant in an AS3 request owned by the instance", func() {
		res := as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, backendUUID), staticBackend(backendUUID)))

		b, err := as3.Document("inst-1", "team1", res.Tenant, true)
		Expect(err).ToNot(HaveOccurred())

		var doc map[string]any
		Expect(json.Unmarshal(b, &doc)).To(Succeed())
		Expect(doc["class"]).To(Equal("AS3"))
		Expect(doc["action"]).To(Equal("dry-run"))
		Expect(doc["persist"]).To(BeTrue())
		decl := doc["declaration"].(map[string]any)
		Expect(decl["class"]).To(Equal("ADC"))
		Expect(decl["schemaVersion"]).To(Equal("3.50.0"))
		Expect(decl["id"]).To(Equal("flock-inst-1"))
		tenant := decl["team1"].(map[string]any)
		Expect(tenant["class"]).To(Equal("Tenant"))
		Expect(tenant["remark"]).To(Equal("flock instance inst-1"))
		app := tenant[as3.AppName(vipUUID)].(map[string]any)
		Expect(app["class"]).To(Equal("Application"))
	})

	It("deploys when not in dry run", func() {
		b, err := as3.Document("inst-1", "flock", as3.Tenant{}, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"action":"deploy"`))
	})

	It("keeps the TCL comparison operators of the iRules as they are", func() {
		in := tlsInput(tlsVS(vsUUID, terminate(backendUUID, "a.example.com")))
		in.VirtualServers[vsUUID].GetTlsTcpRoutesConfig().AcceptProxyProtocol = true

		b, err := as3.Document("inst-1", "flock", as3.Render(in).Tenant, false)

		Expect(err).ToNot(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`$flock_pp_end < 0`))
		Expect(string(b)).To(ContainSubstring(`$flock_pp_addrlen & 0xffff`))
		Expect(string(b)).ToNot(ContainSubstring(`\u003c`))
		Expect(string(b)).ToNot(ContainSubstring(`\u0026`))
	})
})
