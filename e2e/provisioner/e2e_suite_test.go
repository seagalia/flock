//go:build e2e

package provisioner_test

import (
	"context"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
)

// the suite runs on its own subnet, so it never collides with the networks of the other suites
const (
	controllerIP   = "172.31.253.10"
	challtestsrvIP = "172.31.253.20"
	pebbleIP       = "172.31.253.21"
	// in the range of the agent certificates
	provisionerIP       = "172.31.253.100"
	replicaIP           = "172.31.253.101"
	dockerProvisionerIP = "172.31.253.102"
	agentIP             = "172.31.253.103"
	clientIP            = "172.31.253.50"
	// the CN of the certificate of the provisioner, the controller declares it under that name
	provisionerName       = "provisioner-1"
	replicaName           = "provisioner-2"
	dockerProvisionerName = "provisioner-docker"
	agentName             = "agent-1"
	zoneName              = "acme-zone"
	instanceName          = "acme-instance"
	resourceType          = "small"
	vipSubnet             = "10.253.0.0"
	// the controller makes admin the calls made with the certificate of the provisioner, which writes the
	// certificates and secrets it obtains
	provisionerAdminGroup       = "agent-" + provisionerName
	replicaAdminGroup           = "agent-" + replicaName
	dockerProvisionerAdminGroup = "agent-" + dockerProvisionerName
)

// provisionerUuid is the uuid the controller declares the provisioner under and sends it on connection
var provisionerUuid = env.AgentUuid(provisionerName)

var (
	e2eEnv       *env.Env
	controller   *env.Controller
	challtestsrv *env.ChallTestSrv
	pebble       *env.Pebble
	provisioner  *env.Provisioner
	api          *env.Api
	zoneUuid     string
	instanceUuid string
	// the docker specs start them, nil until then
	dockerProvisioner *env.Provisioner
	starlingAgent     *env.Agent
)

// replicas are the provisioners running beside each other, by agent uuid
var replicas = map[string]*env.Provisioner{}

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)
	RunSpecs(t, "flock provisioner e2e Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	var err error
	e2eEnv, err = env.SetupOn(ctx, "172.31.253")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func(ctx SpecContext) {
		e2eEnv.Teardown(ctx)
	}, NodeTimeout(2*time.Minute))

	// every name resolves to the provisioner, which answers the challenges of pebble
	challtestsrv, err = e2eEnv.StartChallTestSrv(ctx, challtestsrvIP, provisionerIP)
	Expect(err).ToNot(HaveOccurred())
	pebble, err = e2eEnv.StartPebble(ctx, pebbleIP, challtestsrvIP)
	Expect(err).ToNot(HaveOccurred())

	controller, err = e2eEnv.StartController(ctx, env.ControllerOptions{
		IP:           controllerIP,
		Zone:         zoneName,
		ResourceType: resourceType,
		Agents: []env.ControllerAgent{
			{Name: provisionerName, IP: provisionerIP, Provisioner: true},
			{Name: replicaName, IP: replicaIP, Provisioner: true},
			{Name: dockerProvisionerName, IP: dockerProvisionerIP, Provisioner: true},
			{Name: agentName, IP: agentIP},
		},
		AdminGroups: []string{provisionerAdminGroup, replicaAdminGroup, dockerProvisionerAdminGroup},
	})
	Expect(err).ToNot(HaveOccurred())
	api, err = controller.LoginAdmin(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(api.Close)

	zoneResp, err := api.Zone.CreateZone(ctx, &svczone.CreateZoneRequest{Zone: &zone.Zone{
		Name:  zoneName,
		Zone:  "z1",
		Pools: []*zone.IpPool{{Name: "default", Subnets: []*zone.Subnet{{Ip: vipSubnet, PrefixLength: 24}}}},
	}})
	Expect(err).ToNot(HaveOccurred())
	zoneUuid = zoneResp.GetZone().GetUUID()
	instanceResp, err := api.Instance.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{Instance: &instance.Instance{
		Name:         instanceName,
		ZoneUuid:     zoneUuid,
		ResourceType: resourceType,
		Metadata:     map[string]string{},
	}})
	Expect(err).ToNot(HaveOccurred())
	instanceUuid = instanceResp.GetInstance().GetUUID()
}, NodeTimeout(15*time.Minute))

// startProvisioner replaces the running provisioner, the new one answers the challenges at the same address
func startProvisioner(ctx context.Context, withTlsAlpn01 bool) {
	GinkgoHelper()
	if provisioner != nil {
		Expect(provisioner.Container.Terminate(ctx)).To(Succeed())
	}
	var err error
	provisioner, err = e2eEnv.StartProvisioner(ctx, env.ProvisionerOptions{
		Name:           provisionerName,
		IP:             provisionerIP,
		Instance:       instanceUuid,
		ControllerAddr: controller.Target(),
		Pebble:         pebble,
		TlsAlpn01:      withTlsAlpn01,
	})
	Expect(err).ToNot(HaveOccurred())
}

var _ = JustAfterEach(func() {
	if !CurrentSpecReport().Failed() {
		return
	}
	dumpCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	AddReportEntry("controller logs", controller.Logs(dumpCtx), types.ReportEntryVisibilityFailureOrVerbose)
	AddReportEntry("pebble logs", pebble.Logs(dumpCtx), types.ReportEntryVisibilityFailureOrVerbose)
	if provisioner != nil {
		AddReportEntry("provisioner logs", provisioner.Logs(dumpCtx), types.ReportEntryVisibilityFailureOrVerbose)
	}
	for agentUuid, replica := range replicas {
		AddReportEntry("replica "+agentUuid+" logs", replica.Logs(dumpCtx), types.ReportEntryVisibilityFailureOrVerbose)
	}
	if dockerProvisioner != nil {
		AddReportEntry("docker provisioner logs", dockerProvisioner.Logs(dumpCtx),
			types.ReportEntryVisibilityFailureOrVerbose)
	}
	if starlingAgent != nil {
		for name, content := range starlingAgent.Dump(dumpCtx) {
			AddReportEntry(name, content, types.ReportEntryVisibilityFailureOrVerbose)
		}
	}
	pause := os.Getenv("E2E_PAUSE_ON_FAILURE")
	if pause == "" {
		return
	}
	duration, err := time.ParseDuration(pause)
	if err != nil {
		duration = 10 * time.Minute
	}
	GinkgoWriter.Printf("E2E_PAUSE_ON_FAILURE: pausing %s\n", duration)
	time.Sleep(duration)
})
