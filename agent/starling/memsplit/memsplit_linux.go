package memsplit

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/pbnjay/memory"
)

const cgroupMount = "/sys/fs/cgroup"

// splitCgroup turns the agent's cgroup into a parent of three leaves and moves the agent into its own. It returns the
// memory of the cgroup before the split, which the shares are computed from.
func (s *Split) splitCgroup(agentPercent uint64, wafExecPercent uint64) (uint64, error) {
	dir, err := agentCgroupDir()
	if err != nil {
		return 0, err
	}
	controllers, err := os.ReadFile(filepath.Join(dir, "cgroup.controllers"))
	if err != nil {
		return 0, fmt.Errorf("read the controllers of %s: %w", dir, err)
	}
	if !strings.Contains(string(controllers), "memory") {
		return 0, fmt.Errorf("the memory controller isn't delegated to %s", dir)
	}
	available := memoryLimit(dir)

	for _, leaf := range []Leaf{agentLeaf, EnvoyLeaf, WafLeaf} {
		err := os.Mkdir(filepath.Join(dir, string(leaf)), 0o755)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return 0, fmt.Errorf("create the cgroup %s: %w", leaf, err)
		}
	}
	err = enableMemoryController(dir)
	if err != nil {
		return 0, err
	}

	agentMax := available * agentPercent / 100
	err = setMemoryLimits(filepath.Join(dir, string(agentLeaf)), agentMax)
	if err != nil {
		return 0, err
	}
	err = setMemoryLimits(filepath.Join(dir, string(WafLeaf)), available*wafExecPercent/100)
	if err != nil {
		return 0, err
	}
	// a previous agent may have set it, envoy keeps itself under its share with its overload manager
	err = setMemoryLimits(filepath.Join(dir, string(EnvoyLeaf)), 0)
	if err != nil {
		return 0, err
	}
	s.cgroupDir = dir
	return available, nil
}

// agentCgroupDir is the cgroup the agent may split. It must hold only the agent and what came with it, the root of a
// container's cgroup namespace or a systemd service with Delegate=yes, since every process in it moves into the
// agent's leaf. A restarted agent already sits in its leaf, the parent of which is the one to split again.
func agentCgroupDir() (string, error) {
	file, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return "", fmt.Errorf("read the cgroup of the agent: %w", err)
	}
	defer file.Close() // nolint:errcheck
	relPath := ""
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		path, isV2 := strings.CutPrefix(scanner.Text(), "0::")
		if isV2 {
			relPath = path
		}
	}
	if relPath == "" {
		return "", errors.New("the agent isn't in a cgroup v2 hierarchy")
	}

	dir := filepath.Join(cgroupMount, relPath)
	isRestarted := filepath.Base(dir) == string(agentLeaf)
	if isRestarted {
		return filepath.Dir(dir), nil
	}
	isContainer := relPath == "/"
	if isContainer {
		return dir, nil
	}
	isService := strings.HasSuffix(relPath, ".service")
	if !isService {
		return "", fmt.Errorf("the cgroup %s isn't dedicated to the agent (container or systemd service)", relPath)
	}
	if !isDelegated(dir) {
		return "", fmt.Errorf("the systemd service of the agent has no Delegate=yes, systemd owns its cgroup %s", relPath)
	}
	return dir, nil
}

// isDelegated tells if systemd gave the cgroup of a service to its processes, which it marks with this attribute
// (systemd 251 and later). Without the delegation, systemd may remove the leaves' controllers at any reload.
func isDelegated(dir string) bool {
	value := make([]byte, 1)
	size, err := syscall.Getxattr(dir, "trusted.delegate", value)
	return err == nil && size == 1 && value[0] == '1'
}

// memoryLimit is the lowest memory.max from dir up to the root of the hierarchy, or the memory of the host.
func memoryLimit(dir string) uint64 {
	limit := memory.TotalMemory()
	for current := dir; strings.HasPrefix(current, cgroupMount); current = filepath.Dir(current) {
		content, err := os.ReadFile(filepath.Join(current, "memory.max"))
		if err != nil {
			continue
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(content)), 10, 64)
		if err != nil {
			continue
		}
		limit = min(limit, value)
	}
	return limit
}

// enableMemoryController moves every process of dir into the agent's leaf, then gives the leaves the memory
// controller, which cgroup v2 refuses while dir holds processes. A process can join dir between the two, a `docker
// exec` or `kubectl exec` for instance, so the move is retried.
func enableMemoryController(dir string) error {
	var err error
	for range 10 {
		err = moveProcesses(dir, filepath.Join(dir, string(agentLeaf)))
		if err != nil {
			return err
		}
		err = os.WriteFile(filepath.Join(dir, "cgroup.subtree_control"), []byte("+memory"), 0)
		if err == nil {
			return nil
		}
	}
	return fmt.Errorf("enable the memory controller of the leaves of %s: %w", dir, err)
}

func moveProcesses(from string, to string) error {
	content, err := os.ReadFile(filepath.Join(from, "cgroup.procs"))
	if err != nil {
		return fmt.Errorf("read the processes of %s: %w", from, err)
	}
	for pid := range strings.FieldsSeq(string(content)) {
		err := os.WriteFile(filepath.Join(to, "cgroup.procs"), []byte(pid), 0)
		if errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return fmt.Errorf("move the process %s into %s: %w", pid, to, err)
		}
	}
	return nil
}

// setMemoryLimits sets memory.high a bit under memory.max, so the kernel reclaims before it kills. 0 is no limit,
// which the WAF's leaf keeps when the WAF doesn't run in it.
func setMemoryLimits(leafDir string, maxBytes uint64) error {
	maxValue := "max"
	highValue := "max"
	if maxBytes > 0 {
		maxValue = strconv.FormatUint(maxBytes, 10)
		highValue = strconv.FormatUint(maxBytes*95/100, 10)
	}
	err := os.WriteFile(filepath.Join(leafDir, "memory.max"), []byte(maxValue), 0)
	if err != nil {
		return fmt.Errorf("set memory.max of %s: %w", leafDir, err)
	}
	err = os.WriteFile(filepath.Join(leafDir, "memory.high"), []byte(highValue), 0)
	if err != nil {
		return fmt.Errorf("set memory.high of %s: %w", leafDir, err)
	}
	return nil
}

// startInLeaf starts cmd right in its cgroup (clone3 with CLONE_INTO_CGROUP), so it never allocates in the agent's.
func (s *Split) startInLeaf(cmd *exec.Cmd, leaf Leaf) error {
	leafDir, err := os.Open(filepath.Join(s.cgroupDir, string(leaf)))
	if err != nil {
		return fmt.Errorf("open the cgroup %s: %w", leaf, err)
	}
	defer leafDir.Close() // nolint:errcheck
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.UseCgroupFD = true
	cmd.SysProcAttr.CgroupFD = int(leafDir.Fd())
	return cmd.Start()
}
