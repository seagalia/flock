//go:build e2e

package provisioner_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"

	"github.com/seagalia/flock/e2e/agent/starling/env"
)

const (
	// the containers of the daemon outside the e2e (a flock dev of the developer) never share it
	composeProject      = "flock-e2e"
	dockerInstanceName  = "docker-instance"
	dockerVipName       = "docker-vip"
	dockerVipIP         = "10.253.0.200"
	webIP               = "172.31.253.30"
	authServiceIP       = "172.31.253.31"
	protectedIP         = "172.31.253.32"
	webDomain           = "web.flock.test"
	authDomain          = "auth.flock.test"
	protectedDomain     = "protected.flock.test"
	authPolicy          = "e2e-sso"
	mdDockerProvisioner = "flock.provisioner.docker"
	noProviderLog       = "no running container provides the external auth " + authPolicy
	httpbinPort         = "8080"
	authStubPort        = "8090"
)

// the provisioner only exposes compose containers, and names their resources after the project and the service
func composeService(service, port string, labels map[string]string) env.ContainerOpt {
	all := map[string]string{
		"com.docker.compose.project":     composeProject,
		"com.docker.compose.service":     service,
		"io.seagalia.flock.enable":       "true",
		"io.seagalia.flock.backend.port": port,
	}
	maps.Copy(all, labels)
	return env.WithLabels(all)
}

func upstreamHeaders(body string) http.Header {
	GinkgoHelper()
	var echo struct {
		Headers http.Header `json:"headers"`
	}
	Expect(json.Unmarshal([]byte(body), &echo)).To(Succeed(), body)
	return echo.Headers
}

var _ = Describe("The docker provisioner", Ordered, func() {
	var (
		client      *env.Client
		authService testcontainers.Container
	)

	getVia := func(ctx SpecContext, domain, path string, opts ...env.CurlOpt) (int, string, error) {
		opts = append(opts, env.WithResolve(domain, 80, dockerVipIP))
		return client.HTTPGet(ctx, "http://"+domain+path, opts...)
	}

	// the docker provisioner and the agent get an instance of their own, the acme provisioners never see its resources
	BeforeAll(func(ctx SpecContext) {
		instanceResp, err := api.Instance.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{Instance: &instance.Instance{
			Name:         dockerInstanceName,
			ZoneUuid:     zoneUuid,
			ResourceType: resourceType,
			Metadata:     map[string]string{},
		}})
		Expect(err).ToNot(HaveOccurred())
		dockerInstanceUuid := instanceResp.GetInstance().GetUUID()
		vipResp, err := api.VIP.CreateVIP(ctx, &svcvip.CreateVIPRequest{Vip: &vip.VIP{
			Name:         dockerVipName,
			Ip:           dockerVipIP,
			PrefixLength: 32,
		}})
		Expect(err).ToNot(HaveOccurred())
		_, err = api.Instance.AddVipToInstance(ctx, &svcinstance.AddVipToInstanceRequest{
			UUID:    dockerInstanceUuid,
			VipUuid: vipResp.GetVip().GetUUID(),
		})
		Expect(err).ToNot(HaveOccurred())

		client, err = e2eEnv.StartClient(ctx, clientIP)
		Expect(err).ToNot(HaveOccurred())
		starlingAgent, err = e2eEnv.StartAgent(ctx, env.AgentOptions{
			Name:           agentName,
			IP:             agentIP,
			Instance:       dockerInstanceUuid,
			ControllerAddr: controller.Target(),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(client.AddRoute(ctx, dockerVipIP+"/32", agentIP)).To(Succeed())
		Eventually(func() (string, error) {
			return starlingAgent.Exec(ctx, "curl", "-s", "http://127.0.0.1:19000/ready")
		}).Should(ContainSubstring("LIVE"))

		dockerProvisioner, err = e2eEnv.StartDockerProvisioner(ctx, env.DockerProvisionerOptions{
			Name:           dockerProvisionerName,
			IP:             dockerProvisionerIP,
			Instance:       dockerInstanceUuid,
			ControllerAddr: controller.Target(),
			Vip:            dockerVipName,
		})
		Expect(err).ToNot(HaveOccurred())
	}, NodeTimeout(10*time.Minute))

	It("serves a labelled http service through the agent", func(ctx SpecContext) {
		_, err := e2eEnv.StartHttpbin(ctx, "web", webIP, composeService("web", httpbinPort, map[string]string{
			"io.seagalia.flock.domains": webDomain,
		}))
		Expect(err).ToNot(HaveOccurred())

		// the agent reaches the container only through the eds of the provisioner, the backend lists no address
		Eventually(func() (string, error) {
			return client.Hostname(ctx, "http://"+webDomain+"/hostname", env.WithResolve(webDomain, 80, dockerVipIP))
		}).Should(Equal("web"))

		resp, err := api.Backend.ListBackend(ctx, &svcbackend.ListBackendRequest{
			Limit: 2,
			Filters: svccore.ListFilters{{
				Key:      storers.FilterNameKey,
				Value:    "io.seagalia.flock.docker/" + composeProject + "-web",
				Operator: svccore.ListFilter_EQUAL,
			}},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.GetBackends()).To(HaveLen(1))
		webBackend := resp.GetBackends()[0]
		Expect(webBackend.GetEndpoint()).To(BeAssignableToTypeOf(&backend.Backend_Eds{}))
		Expect(webBackend.GetMetadata()).To(SatisfyAll(
			HaveKeyWithValue(mdDockerProvisioner, "true"),
			HaveKeyWithValue(helpers.MetadataLockKey, "true"),
			// with acme off no stream tells it its uuid, it provisions under the CN of its certificate
			HaveKeyWithValue(helpers.MetadataProvisionBy, dockerProvisionerName),
		))
	})

	It("leaves out the route of a service whose policy no container provides", func(ctx SpecContext) {
		_, err := e2eEnv.StartHttpbin(ctx, "protected", protectedIP,
			composeService("protected", httpbinPort, map[string]string{
				"io.seagalia.flock.domains":     protectedDomain,
				"io.seagalia.flock.auth.policy": authPolicy,
			}))
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() string { return dockerProvisioner.Logs(ctx) }).Should(ContainSubstring(noProviderLog))
		status, body, err := getVia(ctx, protectedDomain, "/headers", env.WithHeader("x-test-auth: allow"))
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(http.StatusNotFound), body)
	})

	It("checks the requests of the service once an auth service provides the policy", func(ctx SpecContext) {
		var err error
		authService, err = e2eEnv.StartAuthStub(ctx, authServiceIP,
			composeService("auth", authStubPort, map[string]string{
				"io.seagalia.flock.domains":                       authDomain,
				"io.seagalia.flock.auth.provide.name":             authPolicy,
				"io.seagalia.flock.auth.provide.path":             "/check",
				"io.seagalia.flock.auth.provide.request_headers":  "x-test-auth",
				"io.seagalia.flock.auth.provide.upstream_headers": "x-auth-request-user",
			}))
		Expect(err).ToNot(HaveOccurred())

		var body string
		Eventually(func() (int, error) {
			status, answered, err := getVia(ctx, protectedDomain, "/headers", env.WithHeader("x-test-auth: allow"))
			body = answered
			return status, err
		}).Should(Equal(http.StatusOK))
		Expect(upstreamHeaders(body).Values("X-Auth-Request-User")).To(Equal([]string{"alice"}))

		status, body, err := getVia(ctx, protectedDomain, "/headers", env.WithHeader("x-test-auth: deny"))
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(http.StatusUnauthorized))
		Expect(body).To(Equal("denied"))

		status, _, err = getVia(ctx, protectedDomain, "/headers")
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(http.StatusForbidden))
	})

	It("takes the route out again once the auth service stops, never serving it unchecked", func(ctx SpecContext) {
		Expect(e2eEnv.StopContainer(ctx, authService)).To(Succeed())

		Eventually(func() (int, error) {
			status, _, err := getVia(ctx, protectedDomain, "/headers", env.WithHeader("x-test-auth: allow"))
			return status, err
		}).Should(Equal(http.StatusNotFound))
	})
})
