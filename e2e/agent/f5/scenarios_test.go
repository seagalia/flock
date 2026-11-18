//go:build e2e

package f5_test

import (
	"encoding/json"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/agent/f5/bigip/bigipmock"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	f5_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/f5_options/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

const (
	vipUUID     = "11111111-1111-1111-1111-111111111111"
	tcpUUID     = "22222222-2222-2222-2222-222222222221"
	udpUUID     = "22222222-2222-2222-2222-222222222222"
	httpUUID    = "22222222-2222-2222-2222-222222222223"
	tlsUUID     = "22222222-2222-2222-2222-222222222224"
	backendUUID = "33333333-3333-3333-3333-333333333333"
	certUUID    = "44444444-4444-4444-4444-444444444444"
)

func newVIP(partition string, vsUUIDs ...string) *vip.VIP {
	v := &vip.VIP{UUID: vipUUID, Name: "vip-e2e", Ip: "10.99.0.1", VirtualServersUuid: vsUUIDs}
	if partition != "" {
		v.Metadata = map[string]string{"agent.f5.partition": partition}
	}
	return v
}

func newBackend(ips ...string) *backend.Backend {
	var addrs []*core.IPAddr
	for _, ip := range ips {
		addrs = append(addrs, &core.IPAddr{Ip: ip, Port: 8080})
	}
	return &backend.Backend{UUID: backendUUID, Name: "backend-e2e", Endpoint: &backend.Backend_IpAddrGroup{
		IpAddrGroup: &core.IPAddrGroup{IpAddresses: addrs},
	}}
}

func tcpVS(port uint32) *vs.VirtualServer {
	return &vs.VirtualServer{UUID: tcpUUID, Name: "vs-tcp", Port: port, RouteConfig: &vs.VirtualServer_TcpRouteConfig{
		TcpRouteConfig: &vs.TcpRouteConfig{BackendUuid: backendUUID},
	}}
}

func udpVS() *vs.VirtualServer {
	return &vs.VirtualServer{UUID: udpUUID, Name: "vs-udp", Port: 53, RouteConfig: &vs.VirtualServer_UdpRouteConfig{
		UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: backendUUID},
	}}
}

func httpVS() *vs.VirtualServer {
	root := &route.Route{
		Name:   "root",
		Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
		Action: &route.Route_BackendUuid{BackendUuid: backendUUID},
	}
	return &vs.VirtualServer{UUID: httpUUID, Name: "vs-http", Port: 80, RouteConfig: &vs.VirtualServer_HttpRoutesConfig{
		HttpRoutesConfig: &vs.HttpRoutesConfig{VirtualHosts: []*route.VirtualHost{{Domains: []string{"*"}, Routes: []*route.Route{root}}}},
	}}
}

func tlsVS() *vs.VirtualServer {
	terminate := &vs.TlsTcpRouteConfig{BackendUuid: backendUUID, Mode: &vs.TlsTcpRouteConfig_Tls{
		Tls: &core.TLS{ServerNames: []string{"a.example.com"}, CertificatesUuid: []string{certUUID}},
	}}
	return &vs.VirtualServer{UUID: tlsUUID, Name: "vs-tls", Port: 443, RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{
		TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: []*vs.TlsTcpRouteConfig{terminate}},
	}}
}

func newCert() *cert.Certificate {
	chain, err := os.ReadFile(certFile)
	Expect(err).ToNot(HaveOccurred())
	key, err := os.ReadFile(keyFile)
	Expect(err).ToNot(HaveOccurred())
	return &cert.Certificate{UUID: certUUID, Name: "cert-e2e", CertChain: string(chain), PrivateKey: string(key)}
}

func tenantApp(device *bigipmock.Mock, tenant string) map[string]json.RawMessage {
	raw, ok := device.Tenants()[tenant]
	if !ok {
		return nil
	}
	var objects map[string]json.RawMessage
	Expect(json.Unmarshal(raw, &objects)).To(Succeed())
	var app map[string]json.RawMessage
	if appRaw, ok := objects[as3.AppName(vipUUID)]; ok {
		Expect(json.Unmarshal(appRaw, &app)).To(Succeed())
	}
	return app
}

func serviceOf(device *bigipmock.Mock, tenant, vsUUID string) map[string]any {
	raw, ok := tenantApp(device, tenant)[as3.ServiceName(vsUUID)]
	if !ok {
		return nil
	}
	var svc map[string]any
	Expect(json.Unmarshal(raw, &svc)).To(Succeed())
	return svc
}

func deploys(device *bigipmock.Mock, tenant string) int {
	n := 0
	for _, d := range device.Declares() {
		if d.Tenant == tenant && d.Action == "deploy" {
			n++
		}
	}
	return n
}

var _ = Describe("F5 agent", func() {
	var (
		deviceA *bigipmock.Mock
		deviceB *bigipmock.Mock
	)

	BeforeEach(func() {
		controller.RemoveAll(instanceUUID)
		deviceA = bigipmock.New()
		deviceB = bigipmock.New()
		deviceB.SetActive(false)
		DeferCleanup(deviceA.Close)
		DeferCleanup(deviceB.Close)
	})

	deployTCP := func(partition string) {
		GinkgoHelper()
		controller.Push(instanceUUID, newBackend("10.0.0.10"), tcpVS(9000), newVIP(partition, tcpUUID))
		tenant := partition
		if tenant == "" {
			tenant = "flock"
		}
		Eventually(func() map[string]any { return serviceOf(deviceA, tenant, tcpUUID) }).ShouldNot(BeNil())
	}

	It("declares every kind of virtual server in the tenant of the VIP", func() {
		startAgent(deviceA, deviceB)
		controller.Push(instanceUUID, newBackend("10.0.0.10"), newCert(), tcpVS(9000), udpVS(), httpVS(), tlsVS(),
			newVIP("", tcpUUID, udpUUID, httpUUID, tlsUUID))

		Eventually(func() []string {
			var found []string
			for _, uuid := range []string{tcpUUID, udpUUID, httpUUID, tlsUUID} {
				if svc := serviceOf(deviceA, "flock", uuid); svc != nil {
					found = append(found, svc["class"].(string))
				}
			}
			return found
		}).Should(Equal([]string{"Service_TCP", "Service_UDP", "Service_HTTP", "Service_TCP"}))
		Expect(serviceOf(deviceA, "flock", tlsUUID)).To(HaveKey("serverTLS"))
		Expect(deviceB.Tenants()).To(BeEmpty(), "the standby device gets its config from the HA sync")
	})

	It("posts a virtual server update once", func() {
		startAgent(deviceA, deviceB)
		deployTCP("")
		deviceA.ResetDeclares()

		controller.Push(instanceUUID, tcpVS(9001))

		Eventually(func() any { return serviceOf(deviceA, "flock", tcpUUID)["virtualPort"] }).Should(BeNumerically("==", 9001))
		Consistently(func() int { return deploys(deviceA, "flock") }, 2*time.Second).Should(Equal(1))
	})

	It("moves a VIP to the partition named in its metadata and warns about the interruption", func() {
		p := startAgent(deviceA, deviceB)
		deployTCP("")

		controller.Push(instanceUUID, newVIP("apps", tcpUUID))

		Eventually(func() map[string]any { return serviceOf(deviceA, "apps", tcpUUID) }).ShouldNot(BeNil())
		Eventually(deviceA.Tenants).ShouldNot(HaveKey("flock"))
		resources, _ := p.statusClients()
		Eventually(func() []string {
			ctx, cancel := withTimeout(5 * time.Second)
			defer cancel()
			status, err := resources.GetStatus(ctx, &agentstatus.GetStatusRequest{
				TypeUrl: helpers.TypeUrl[*vip.VIP](), Identifier: vipUUID,
			})
			if err != nil {
				return nil
			}
			return noteMessages(status.GetNotes())
		}).Should(ContainElement(ContainSubstring("moved from partition flock")))
	})

	It("keeps the pool of a backend the controller removed while a virtual server references it", func() {
		startAgent(deviceA, deviceB)
		deployTCP("")
		before := deviceA.Tenants()["flock"]
		deviceA.ResetDeclares()

		controller.Remove(instanceUUID, helpers.TypeUrl[*backend.Backend](), backendUUID)

		Consistently(func() json.RawMessage { return deviceA.Tenants()["flock"] }, 3*time.Second).Should(Equal(before))
		Expect(deploys(deviceA, "flock")).To(BeZero())

		controller.Push(instanceUUID, newBackend("10.0.0.11"))
		Eventually(func() string { return string(deviceA.Tenants()["flock"]) }).Should(ContainSubstring("10.0.0.11"))
	})

	It("posts to the other device of the pair after a failover", func() {
		startAgent(deviceA, deviceB)
		deployTCP("")

		deviceA.SetActive(false)
		deviceB.SetActive(true)
		// the client remembers the active device for 10s
		time.Sleep(11 * time.Second)
		controller.Push(instanceUUID, tcpVS(9002))

		Eventually(func() any { return serviceOf(deviceB, "flock", tcpUUID)["virtualPort"] }).Should(BeNumerically("==", 9002))
		Expect(serviceOf(deviceA, "flock", tcpUUID)["virtualPort"]).To(BeNumerically("==", 9000))
	})

	It("reports the options it ignores and the devices through the agent status", func() {
		p := startAgent(deviceA, deviceB)
		withOptions := tcpVS(9000)
		options, err := anypb.New(&f5_vsopts.VirtualServerOptions{HttpProfile: "/Common/http"})
		Expect(err).ToNot(HaveOccurred())
		withOptions.GetTcpRouteConfig().TypedOptions = options
		controller.Push(instanceUUID, newBackend("10.0.0.10"), withOptions, newVIP("", tcpUUID))

		resources, agents := p.statusClients()
		Eventually(func() []string {
			ctx, cancel := withTimeout(5 * time.Second)
			defer cancel()
			status, err := resources.GetStatus(ctx, &agentstatus.GetStatusRequest{
				TypeUrl: helpers.TypeUrl[*vs.VirtualServer](), Identifier: tcpUUID,
			})
			if err != nil {
				return nil
			}
			return noteMessages(status.GetNotes())
		}).Should(ContainElement("http_profile only applies to http virtual servers, it is ignored"))

		ctx, cancel := withTimeout(5 * time.Second)
		defer cancel()
		status, err := agents.GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{ShowAnnouncingVips: true})
		Expect(err).ToNot(HaveOccurred())
		components := map[string]agent.Status{}
		for _, component := range status.GetProxyAgentState().GetComponentStates() {
			components[component.GetName()] = component.GetStatus()
		}
		Expect(components).To(HaveKeyWithValue("bigip "+deviceA.URL(), agent.Status_OK))
		Expect(components).To(HaveKeyWithValue("bigip "+deviceB.URL(), agent.Status_WARNING), "the standby device")
		Expect(components).To(HaveKeyWithValue("reconciler", agent.Status_OK))
		announcing := status.GetProxyAgentState().GetComponentAnnouncingVips()
		Expect(announcing).To(HaveLen(1))
		Expect(announcing[0].GetName()).To(Equal("bigip " + deviceA.URL()))
	})

	It("sweeps the tenants of the instance no VIP uses once the controller delivered the resources", func() {
		deviceA.AddTenant("old", as3.TenantRemark(instanceUUID))
		deviceA.AddTenant("other", "someone else")
		startAgent(deviceA, deviceB)
		deployTCP("")

		// garbage_after_start is 3s
		Eventually(deviceA.Tenants).ShouldNot(HaveKey("old"))
		Expect(deviceA.Tenants()).To(HaveKey("other"))
		Expect(deviceA.Tenants()).To(HaveKey("flock"))
	})

	It("leaves the tenants as they are after a restart", func() {
		p := startAgent(deviceA, deviceB)
		deployTCP("")
		before := deviceA.Tenants()

		p.restart()

		// garbage_after_start is 3s: past it the orphan sweep ran and found nothing to delete
		Consistently(deviceA.Tenants, 5*time.Second).Should(Equal(before))
	})
})

func noteMessages(notes []*flocktype.Note) []string {
	messages := make([]string, 0, len(notes))
	for _, note := range notes {
		messages = append(messages, note.GetMessage())
	}
	return messages
}
