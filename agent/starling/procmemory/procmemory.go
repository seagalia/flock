// Package procmemory gives the memory a process can use, which the agent, envoy and the WAF size themselves with.
package procmemory

import (
	"github.com/KimMachineGun/automemlimit/memlimit"
	"github.com/pbnjay/memory"
)

// Available is the memory limit of the cgroup of the process, or the memory of the host when the process has no
// limit, isn't in a cgroup, or runs on another OS than Linux. A child process shares the cgroup of its parent, so a
// child started by the agent has the same limit as the agent.
func Available() uint64 {
	hostMemory := memory.TotalMemory()
	cgroupLimit, err := memlimit.FromCgroup()
	if err != nil || cgroupLimit == 0 {
		return hostMemory
	}
	return min(hostMemory, cgroupLimit)
}
