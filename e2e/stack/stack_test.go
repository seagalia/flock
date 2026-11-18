//go:build e2e

package stack_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	agentapi "github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	corev1 "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/seagalia/flock/e2e/agent/starling/env"
)

// createWithCli runs a create command of the cli with manifest and gives what the controller created
func createWithCli[T proto.Message](ctx SpecContext, command, manifest string, created T) T {
	GinkgoHelper()
	path := writeManifest(command+".yml", manifest)
	out, err := flockCli(ctx, command, "-f", path, "--force", "--json")
	Expect(err).ToNot(HaveOccurred(), out)
	Expect(protojson.Unmarshal([]byte(out), created)).To(Succeed(), out)
	return created
}

var _ = Describe("A controller with a real agent", Ordered, func() {
	const (
		vipIP = "10.251.0.10"
		url   = "http://" + vipIP + "/hostname"
	)
	var (
		createdBackend *backend.Backend
		createdVs      *vs.VirtualServer
		createdVip     *vip.VIP
	)

	It("serves a virtual server created with the cli", func(ctx SpecContext) {
		createdBackend = createWithCli(ctx, "create-backend", fmt.Sprintf(`name: stack-web
ip_addr_group:
  ip_addresses:
  - ip: %s
    port: 8080
health_check:
  timeout: 1s
  interval: 5s
  no_health_check: {}
refresh_rate: 5s
`, httpbin1IP), &backend.Backend{})

		createdVs = createWithCli(ctx, "create-vs", fmt.Sprintf(`name: stack-vs
port: 80
http_routes_config:
  virtual_hosts:
  - domains: ['*']
    routes:
    - name: default
      match:
        prefix: /
      backend_uuid: %s
`, createdBackend.GetUUID()), &vs.VirtualServer{})

		createdVip = createWithCli(ctx, "create-vip", fmt.Sprintf(`name: stack-vip
ip: %s
prefix_length: 32
typed_announce:
  '@type': type.googleapis.com/flock.extensions.announces.starling_announce.v1.StarlingAnnounce
  type_announce: STATIC
virtual_servers_uuid:
- %s
`, vipIP, createdVs.GetUUID()), &vip.VIP{})

		out, err := flockCli(ctx, "add-vip-to-instance", instanceName, createdVip.GetName())
		Expect(err).ToNot(HaveOccurred(), out)

		Eventually(func() (string, error) { return client.Hostname(ctx, url) }, "1m").Should(Equal("httpbin-1"))
	})

	It("applies a backend update made through the api", func(ctx SpecContext) {
		updated := proto.CloneOf(createdBackend)
		updated.Endpoint = &backend.Backend_IpAddrGroup{IpAddrGroup: &corev1.IPAddrGroup{
			IpAddresses: []*corev1.IPAddr{{Ip: httpbin2IP, Port: 8080}},
		}}
		_, err := api.Backend.UpdateBackend(ctx, &svcbackend.UpdateBackendRequest{UUID: updated.GetUUID(), Backend: updated})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() (string, error) { return client.Hostname(ctx, url) }).Should(Equal("httpbin-2"))
	})

	It("lets an operator in with a break-glass token of the controller host", func(ctx SpecContext) {
		token, err := controller.AdminToken(ctx, "e2e")
		Expect(err).ToNot(HaveOccurred())

		out, err := flockCliWithEnv(ctx, []string{"FLOCK_TOKEN=" + token}, "me", "--json")
		Expect(err).ToNot(HaveOccurred(), out)
		me := struct {
			Username string `json:"username"`
			IsAdmin  bool   `json:"is_admin"`
		}{}
		Expect(json.Unmarshal([]byte(out), &me)).To(Succeed(), out)
		Expect(me.Username).To(Equal(svclogin.BreakGlassUsername))
		Expect(me.IsAdmin).To(BeTrue())
		Eventually(func() string { return controller.Logs(ctx) }).Should(And(
			ContainSubstring("break-glass access"),
			ContainSubstring("reason=e2e"),
		))
	})

	It("lists the agent of the instance with the cli", func(ctx SpecContext) {
		Eventually(func() (string, error) { return flockCli(ctx, "agents", instanceName, "--json") }).Should(ContainSubstring(agentName))
	})

	It("relays the status of the real agent", func(ctx SpecContext) {
		Eventually(func(g Gomega) {
			resp, err := api.Lookup.ListAgentStatus(ctx, &svclookup.ListAgentStatusRequest{
				InstanceUuid: instanceUuid, ShowAnnouncingVips: true,
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetAgentStatus()).To(HaveLen(1))
			agentStatus := resp.GetAgentStatus()[0]
			g.Expect(agentStatus.GetAgentUuid()).To(Equal(agentUuid))
			sidecar := agentStatus.GetAgentStatus().GetSidecarAgentState()
			g.Expect(sidecar.GetAgentState().GetStatus()).To(Equal(agentapi.Status_OK))
			announced := sidecar.GetAnnouncingVips()
			g.Expect(announced).To(ContainElement(HaveField("UUID", createdVip.GetUUID())))
		}).Should(Succeed())
	})

	It("reports the data path of a tcp virtual server", func(ctx SpecContext) {
		tcpVs := createWithCli(ctx, "create-vs", fmt.Sprintf(`name: stack-tcp
port: 9000
tcp_route_config:
  backend_uuid: %s
`, createdBackend.GetUUID()), &vs.VirtualServer{})
		stored, err := api.VIP.ReadVIP(ctx, &svccore.UuidRequest{UUID: createdVip.GetUUID()})
		Expect(err).ToNot(HaveOccurred())
		storedVip := stored.GetVip()
		withTcp := proto.CloneOf(storedVip)
		withTcp.VirtualServersUuid = append(withTcp.VirtualServersUuid, tcpVs.GetUUID())
		_, err = api.VIP.UpdateVIP(ctx, &svcvip.UpdateVIPRequest{UUID: withTcp.GetUUID(), Vip: withTcp})
		Expect(err).ToNot(HaveOccurred())
		// Later scenarios expect the resources of the first one alone.
		DeferCleanup(func(ctx SpecContext) {
			_, err := api.VIP.UpdateVIP(ctx, &svcvip.UpdateVIPRequest{UUID: storedVip.GetUUID(), Vip: storedVip})
			Expect(err).ToNot(HaveOccurred())
			_, err = api.VS.DeleteVS(ctx, &svccore.UuidRequest{UUID: tcpVs.GetUUID()})
			Expect(err).ToNot(HaveOccurred())
		})

		Eventually(func(g Gomega) {
			resp, err := api.Lookup.ListStatus(ctx, &svclookup.ListStatusRequest{
				InstanceUuid: instanceUuid, TypeUrl: helpers.TypeUrl[*vs.VirtualServer](),
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetStatus()).To(ContainElement(HaveField("Notes", ContainElement(SatisfyAll(
				HaveField("Code", HavePrefix("starling.datapath.")),
				HaveField("Scope", vipIP+":9000/tcp"),
			)))))
		}, "1m").Should(Succeed())
	})

	It("relays the logs of the real agent", func(ctx SpecContext) {
		resp, err := api.Lookup.GetLogsLatest(ctx, &svclookup.GetLogsRequest{InstanceUuid: instanceUuid})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.GetLogs()).To(HaveLen(1))
		Expect(resp.GetLogs()[0].GetAgentUuid()).To(Equal(agentUuid))
		Expect(resp.GetLogs()[0].GetLogLines()).ToNot(BeEmpty())
	})

	It("proxies the metrics of the real agent", func(ctx SpecContext) {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(e2eEnv.Certs.CAPem)
		httpClient := &http.Client{
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost"}},
			Timeout:   30 * time.Second,
		}
		resp, err := httpClient.Get("https://" + controller.HostAddr(ctx) + "/metrics/" + instanceUuid)
		Expect(err).ToNot(HaveOccurred())
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK), string(body))
		Expect(string(body)).To(MatchRegexp(`flock_agent_\w+\{[^}]*agent_uuid="` + agentUuid + `"`))
	})

	It("applies a resource file with the cli", func(ctx SpecContext) {
		// Port 81 keeps the route of the first scenario untouched. The vip document replaces the whole VIP, so it
		// starts from the stored one.
		stored, err := api.VIP.ReadVIP(ctx, &svccore.UuidRequest{UUID: createdVip.GetUUID()})
		Expect(err).ToNot(HaveOccurred())
		storedVip := stored.GetVip()
		vipFields, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(storedVip)
		Expect(err).ToNot(HaveOccurred())
		vipDoc := map[string]any{}
		Expect(json.Unmarshal(vipFields, &vipDoc)).To(Succeed())
		// Without prefix_length the second apply only finds no change when both sides default it the same way.
		delete(vipDoc, "uuid")
		delete(vipDoc, "prefix_length")
		vipDoc["kind"] = "vip"
		vipDoc["virtual_servers_uuid"] = []string{createdVs.GetUUID(), "name:stack-applied-vs"}
		vipJson, err := json.Marshal(vipDoc)
		Expect(err).ToNot(HaveOccurred())

		file := writeManifest("resources.yml", fmt.Sprintf(`kind: backend
name: stack-applied-web
ip_addr_group:
  ip_addresses:
  - ip: %s
    port: 8080
health_check:
  timeout: 1s
  interval: 5s
  no_health_check: {}
refresh_rate: 5s
---
kind: vs
name: stack-applied-vs
port: 81
http_routes_config:
  virtual_hosts:
  - domains: ['*']
    routes:
    - name: default
      match:
        prefix: /
      backend_uuid: name:stack-applied-web
---
%s
`, httpbin1IP, vipJson))

		// Later scenarios expect the resources of the first one alone, even when this one fails halfway.
		DeferCleanup(func(ctx SpecContext) {
			_, err := api.VIP.UpdateVIP(ctx, &svcvip.UpdateVIPRequest{UUID: storedVip.GetUUID(), Vip: storedVip})
			Expect(err).ToNot(HaveOccurred())
			nameIs := []*svccore.ListFilter{{Key: "name", Value: "stack-applied-vs", Operator: svccore.ListFilter_EQUAL}}
			listedVs, err := api.VS.ListVS(ctx, &svcvs.ListVSRequest{Limit: 10, Filters: nameIs})
			Expect(err).ToNot(HaveOccurred())
			for _, appliedVs := range listedVs.GetVs() {
				_, err = api.VS.DeleteVS(ctx, &svccore.UuidRequest{UUID: appliedVs.GetUUID()})
				Expect(err).ToNot(HaveOccurred())
			}
			nameIs = []*svccore.ListFilter{{Key: "name", Value: "stack-applied-web", Operator: svccore.ListFilter_EQUAL}}
			listedBackends, err := api.Backend.ListBackend(ctx, &svcbackend.ListBackendRequest{Limit: 10, Filters: nameIs})
			Expect(err).ToNot(HaveOccurred())
			for _, appliedBackend := range listedBackends.GetBackends() {
				_, err = api.Backend.DeleteBackend(ctx, &svccore.UuidRequest{UUID: appliedBackend.GetUUID()})
				Expect(err).ToNot(HaveOccurred())
			}
		})

		_, stderr, err := flockCliAdminStreams(ctx, "apply", "-f", file, "--yes")
		Expect(err).ToNot(HaveOccurred(), stderr)

		Eventually(func() (string, error) { return client.Hostname(ctx, "http://"+vipIP+":81/hostname") }, "1m").
			Should(Equal("httpbin-1"))

		stdout, stderr, err := flockCliAdminStreams(ctx, "apply", "-f", file, "--yes")
		Expect(err).ToNot(HaveOccurred(), stderr)
		Expect(stdout + stderr).To(ContainSubstring("Nothing to change"))
	})

	It("refuses to change a resource locked by flock.autoprovision", func(ctx SpecContext) {
		locked := &backend.Backend{
			Name:     "stack-locked",
			Metadata: map[string]string{"flock.autoprovision": "true"},
			HealthCheck: &corev1.HealthCheck{
				Timeout:       durationpb.New(time.Second),
				Interval:      durationpb.New(5 * time.Second),
				HealthChecker: &corev1.HealthCheck_NoHealthCheck{NoHealthCheck: &corev1.NoHealthCheck{}},
			},
			Endpoint: &backend.Backend_IpAddrGroup{IpAddrGroup: &corev1.IPAddrGroup{
				IpAddresses: []*corev1.IPAddr{{Ip: "10.0.0.9", Port: 8080}},
			}},
		}
		created, err := api.Backend.CreateBackend(ctx, &svcbackend.CreateBackendRequest{Backend: locked})
		Expect(err).ToNot(HaveOccurred())
		lockedUuid := created.GetBackend().GetUUID()
		DeferCleanup(func(ctx SpecContext) {
			_, err := api.Backend.DeleteBackend(ctx, &svccore.UuidRequest{UUID: lockedUuid})
			Expect(err).ToNot(HaveOccurred())
		})
		lockText := "Managed automatically (metadata flock.autoprovision). Change it where it was provisioned."

		file := writeManifest("locked-backend.yml", `name: stack-locked
ip_addr_group:
  ip_addresses:
  - ip: 10.0.0.9
    port: 8080
health_check:
  timeout: 1s
  interval: 5s
  no_health_check: {}
refresh_rate: 5s
`)
		out, err := flockCli(ctx, "update-backend", "stack-locked", "-f", file, "--force")
		Expect(err).To(HaveOccurred())
		Expect(out).To(ContainSubstring(lockText))

		out, err = flockCli(ctx, "delete-backend", "stack-locked", "--force")
		Expect(err).To(HaveOccurred())
		Expect(out).To(ContainSubstring(lockText))

		stored, err := api.Backend.ReadBackend(ctx, &svccore.UuidRequest{UUID: lockedUuid})
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(stored.GetBackend(), created.GetBackend())).To(BeTrue(), "the backend changed")
	})

	It("cleans the agent when the resources are deleted through the api", func(ctx SpecContext) {
		_, err := api.Instance.DeleteVipFromInstance(ctx, &svcinstance.DeleteVipFromInstanceRequest{
			UUID: instanceUuid, VipUuid: createdVip.GetUUID(),
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = api.VIP.DeleteVIP(ctx, &svccore.UuidRequest{UUID: createdVip.GetUUID()})
		Expect(err).ToNot(HaveOccurred())
		_, err = api.VS.DeleteVS(ctx, &svccore.UuidRequest{UUID: createdVs.GetUUID()})
		Expect(err).ToNot(HaveOccurred())
		_, err = api.Backend.DeleteBackend(ctx, &svccore.UuidRequest{UUID: createdBackend.GetUUID()})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() []string { return agent.EnvoyListeners(ctx) }).Should(BeEmpty())
		Eventually(func() []string { return agent.EnvoyClusters(ctx) }).Should(ConsistOf("xds_agent", "ext_proc_cluster"))
		Eventually(func() []env.IpvsService { return agent.Ipvs(ctx) }).Should(BeEmpty())
		Eventually(func() []string { return agent.LocalRoutes(ctx) }).ShouldNot(ContainElement(vipIP + "/32"))
		Eventually(func() error {
			_, err := client.Hostname(ctx, url)
			return err
		}).Should(HaveOccurred())
	})
})
