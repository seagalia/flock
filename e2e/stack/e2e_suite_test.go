//go:build e2e

package stack_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

// the stack runs on its own subnet, so it never collides with the network of the agent suite
const (
	controllerIP = "172.31.251.10"
	httpbin1IP   = "172.31.251.11"
	httpbin2IP   = "172.31.251.12"
	clientIP     = "172.31.251.50"
	agentIP      = "172.31.251.100"
	agentName    = "agent-1"
	zoneName     = "stack-zone"
	instanceName = "stack-instance"
	resourceType = "small"
	// vips are routed to the agent by the client, they are taken in the pool of the zone
	vipSubnet = "10.251.0.0"
	vipCidr   = vipSubnet + "/24"
)

// agentUuid is the uuid the controller declares the agent under, the agent is named by the CN of its certificate
var agentUuid = env.AgentUuid(agentName)

var (
	e2eEnv       *env.Env
	controller   *env.Controller
	api          *env.Api
	agent        *env.Agent
	client       *env.Client
	instanceUuid string
	cliBin       string
	cliHome      string
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(30 * time.Second)
	SetDefaultEventuallyPollingInterval(500 * time.Millisecond)
	RunSpecs(t, "flock stack e2e Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	var err error
	e2eEnv, err = env.SetupOn(ctx, "172.31.251")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func(ctx SpecContext) {
		e2eEnv.Teardown(ctx)
	}, NodeTimeout(2*time.Minute))

	cliBin, cliHome = buildCli(ctx)

	_, err = e2eEnv.StartHttpbin(ctx, "httpbin-1", httpbin1IP)
	Expect(err).ToNot(HaveOccurred())
	_, err = e2eEnv.StartHttpbin(ctx, "httpbin-2", httpbin2IP)
	Expect(err).ToNot(HaveOccurred())
	client, err = e2eEnv.StartClient(ctx, clientIP)
	Expect(err).ToNot(HaveOccurred())

	controller, err = e2eEnv.StartController(ctx, env.ControllerOptions{
		IP:           controllerIP,
		Zone:         zoneName,
		ResourceType: resourceType,
		Agents:       []env.ControllerAgent{{Name: agentName, IP: agentIP}},
	})
	Expect(err).ToNot(HaveOccurred())
	api, err = controller.LoginAdmin(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(api.Close)

	// the agent needs the uuid of its instance in its config, so the instance exists before the agent
	zoneResp, err := api.Zone.CreateZone(ctx, &svczone.CreateZoneRequest{Zone: &zone.Zone{
		Name:  zoneName,
		Zone:  "z1",
		Pools: []*zone.IpPool{{Name: "default", Subnets: []*zone.Subnet{{Ip: vipSubnet, PrefixLength: 24}}}},
	}})
	Expect(err).ToNot(HaveOccurred())
	instanceResp, err := api.Instance.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{Instance: &instance.Instance{
		Name:         instanceName,
		ZoneUuid:     zoneResp.GetZone().GetUUID(),
		ResourceType: resourceType,
		Metadata:     map[string]string{},
	}})
	Expect(err).ToNot(HaveOccurred())
	instanceUuid = instanceResp.GetInstance().GetUUID()

	agent, err = e2eEnv.StartAgent(ctx, env.AgentOptions{
		Name:           agentName,
		IP:             agentIP,
		Instance:       instanceUuid,
		ControllerAddr: controller.Target(),
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(client.AddRoute(ctx, vipCidr, agentIP)).To(Succeed())
	Eventually(func() (string, error) {
		return agent.Exec(ctx, "curl", "-s", "http://127.0.0.1:19000/ready")
	}, 2*time.Minute).Should(ContainSubstring("LIVE"))
}, NodeTimeout(15*time.Minute))

// buildCli builds the flock binary of the host, the cli gets a home of its own so it never reads the session
// or the `flock dev` pid of the user
func buildCli(ctx context.Context) (bin, home string) {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	bin = filepath.Join(dir, "flock")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := build.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "build cli: %s", out)
	home = filepath.Join(dir, "home")
	Expect(os.MkdirAll(home, 0o755)).To(Succeed())
	return bin, home
}

// flockCli runs a cli command as the admin, the client is built from the FLOCK_* variables alone. It gives the
// stdout of the command, and its stderr too when it fails.
func flockCli(ctx context.Context, args ...string) (string, error) {
	adminEnv := []string{"FLOCK_USERNAME=" + env.ControllerAdmin, "FLOCK_PASSWORD=" + env.ControllerPassword}
	return flockCliWithEnv(ctx, adminEnv, args...)
}

// flockCliWithEnv runs a cli command with the credentials or the token of extraEnv
func flockCliWithEnv(ctx context.Context, extraEnv []string, args ...string) (string, error) {
	stdout, stderr, err := flockCliStreams(ctx, extraEnv, args...)
	if err != nil {
		return stdout + stderr, err
	}
	return strings.TrimSpace(stdout), nil
}

// flockCliAdminStreams runs a cli command as the admin and gives stdout and stderr apart, for the commands that
// report on stderr
func flockCliAdminStreams(ctx context.Context, args ...string) (stdout, stderr string, err error) {
	adminEnv := []string{"FLOCK_USERNAME=" + env.ControllerAdmin, "FLOCK_PASSWORD=" + env.ControllerPassword}
	return flockCliStreams(ctx, adminEnv, args...)
}

// flockCliStreams runs a cli command in the cli home with only the FLOCK_* variables of extraEnv, so the developer's
// own config never leaks into a spec
func flockCliStreams(ctx context.Context, extraEnv []string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, cliBin, args...)
	cmd.Dir = cliHome
	cmd.Env = append(envWithoutFlock(),
		"HOME="+cliHome,
		"XDG_CACHE_HOME="+filepath.Join(cliHome, ".cache"),
		"FLOCK_CLI_CONFIG="+filepath.Join(cliHome, "config.json"),
		"FLOCK_HOST="+controller.HostAddr(ctx),
		"FLOCK_SKIP_SSL_VALIDATION=1",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func writeManifest(name, content string) string {
	GinkgoHelper()
	path := filepath.Join(cliHome, name)
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
	return path
}

var _ = JustAfterEach(func() {
	if !CurrentSpecReport().Failed() {
		return
	}
	dumpCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	AddReportEntry("controller logs", controller.Logs(dumpCtx), types.ReportEntryVisibilityFailureOrVerbose)
	for name, content := range agent.Dump(dumpCtx) {
		AddReportEntry(name, content, types.ReportEntryVisibilityFailureOrVerbose)
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

// envWithoutFlock gives the environment of the test process without its FLOCK_* variables: a developer's own would
// override the ones the cli is given here.
func envWithoutFlock() []string {
	return slices.DeleteFunc(os.Environ(), func(entry string) bool { return strings.HasPrefix(entry, "FLOCK_") })
}
