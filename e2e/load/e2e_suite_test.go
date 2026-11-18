//go:build load

package load_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/e2e/load/measure"
)

// the knobs are read before any container starts, a bad value fails the run at once
var (
	ctlVs, ctlBackends, ctlVips, ctlCerts, ctlAgents, ctlApiClients int
	ctlApiFor, ctlChurnFor                                          time.Duration

	agentVs, agentBackends, agentEndpoints, agentConns int
	agentHoldFor, agentChurnFor                        time.Duration
)

var (
	repoRoot string
	report   measure.Report
)

func TestLoad(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(5 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)
	RunSpecs(t, "flock load Suite")
}

var _ = BeforeSuite(func() {
	// first, the report of a run failing on a knob still goes to the repository root
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	Expect(err).NotTo(HaveOccurred())
	repoRoot = strings.TrimSpace(string(out))

	ctlVs = knob("CTL_VS", 2000)
	ctlBackends = knob("CTL_BACKENDS", 2000)
	ctlVips = knob("CTL_VIPS", 500)
	ctlCerts = knob("CTL_CERTS", 200)
	ctlAgents = knob("CTL_AGENTS", 200)
	ctlApiClients = knob("CTL_API_CLIENTS", 50)
	ctlApiFor = duration("CTL_API", time.Minute)
	ctlChurnFor = duration("CTL_CHURN", 5*time.Minute)

	agentVs = knob("AGENT_VS", 2000)
	agentBackends = knob("AGENT_BACKENDS", 200)
	agentEndpoints = knob("AGENT_ENDPOINTS", 50)
	agentConns = knob("AGENT_CONNS", 5000)
	agentHoldFor = duration("AGENT_HOLD", time.Minute)
	agentChurnFor = duration("AGENT_CHURN", 5*time.Minute)

})

// the report is written even when a phase failed, the rows measured so far are still worth reading
var _ = AfterSuite(func() {
	if repoRoot == "" {
		return
	}
	report.Date = time.Now()
	report.Scale = measure.Scale()
	report.Commit = "unknown"
	commit, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err == nil {
		report.Commit = strings.TrimSpace(string(commit))
	}
	rendered := report.Render()
	GinkgoWriter.Println(rendered)

	path := filepath.Join(repoRoot, "e2e", ".build", "load-report.md")
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(rendered), 0o644)).To(Succeed())
	GinkgoWriter.Printf("report written to %s\n", path)
})
