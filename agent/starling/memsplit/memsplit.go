// Package memsplit shares the memory of the node between the agent, envoy and the WAF. On Linux with cgroup v2, it
// also gives each of them its own cgroup, so the kernel holds the agent and the WAF to their share and envoy's
// overload manager watches envoy alone.
package memsplit

import (
	"os/exec"

	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/agent/starling/procmemory"
	"github.com/seagalia/flock/sdk/logging"
)

// AgentPercent is the share of the memory the agent never goes over, its own work being the xDS control plane.
const AgentPercent = 20

// Leaf is the cgroup of one process, beside the agent's.
type Leaf string

const (
	EnvoyLeaf Leaf = "flock-envoy"
	WafLeaf   Leaf = "starling-waf"
	agentLeaf Leaf = "flock-agent"
)

type Split struct {
	// Envoy is the memory envoy's overload manager keeps envoy under, what the agent and the WAF leave.
	Envoy uint64
	// Waf is the memory of an embedded or exec WAF, 0 when the WAF runs elsewhere.
	Waf uint64
	// Agent is the memory of the agent, an embedded WAF included.
	Agent uint64
	// EnvoyPercent is the share Envoy is of the available memory, for an envoy service, which has its own cgroup.
	EnvoyPercent uint64
	// cgroupDir is the cgroup holding the leaves, empty when the processes share the agent's cgroup.
	cgroupDir string
}

// New computes the shares and, when the agent owns its cgroup, splits it. Without the split, the shares still hold
// for envoy and the WAF, which size themselves, and for the agent through its Go memory limit.
func New(wafConfig *config.WafConfig, devMode bool) *Split {
	entry := logging.Component("memsplit")
	isWafLocal := wafConfig.WafEmbedded || wafConfig.WafExec != nil
	wafPercent := uint64(0)
	if isWafLocal {
		wafPercent = config.WafMemoryPercent
	}
	agentPercent := uint64(AgentPercent)
	wafExecPercent := uint64(0)
	if wafConfig.WafEmbedded {
		agentPercent += config.WafMemoryPercent
	}
	if wafConfig.WafExec != nil {
		wafExecPercent = config.WafMemoryPercent
	}
	split := &Split{EnvoyPercent: 100 - AgentPercent - wafPercent}

	// dev mode runs on a laptop and leaves the cgroups of its host alone, like its network
	var err error
	available := procmemory.Available()
	if !devMode {
		available, err = split.splitCgroup(agentPercent, wafExecPercent)
	}
	if err != nil {
		entry.Warn().Err(err).Msg("Envoy, the WAF and the agent share the agent's cgroup, " +
			"so the kernel doesn't hold the agent and the WAF to their share of the memory")
		available = procmemory.Available()
	}
	split.Agent = available * agentPercent / 100
	split.Waf = available * wafPercent / 100
	split.Envoy = available * split.EnvoyPercent / 100

	entry.Info().
		Bool("cgroups", split.HasCgroups()).
		Uint64("agent_bytes", split.Agent).
		Uint64("waf_bytes", split.Waf).
		Uint64("envoy_bytes", split.Envoy).
		Msg("Memory shared between the agent, the WAF and envoy")
	return split
}

// HasCgroups tells if envoy and an exec WAF start in their own cgroup.
func (s *Split) HasCgroups() bool {
	return s.cgroupDir != ""
}

// Start starts cmd in its leaf, or in the agent's cgroup when there is no split.
func (s *Split) Start(cmd *exec.Cmd, leaf Leaf) error {
	if !s.HasCgroups() {
		return cmd.Start()
	}
	return s.startInLeaf(cmd, leaf)
}
