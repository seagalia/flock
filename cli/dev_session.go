package cli

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Files of the session started by `flock dev`, the cli uses DevCliConfigPath while that session runs.
const (
	DevCliConfigPath = "~/.flock/dev/cli-config.json"
	DevPidPath       = "~/.flock/dev/dev.pid"
)

// devSessionRunning tells if a `flock dev` process is alive, a pid file left by a killed session is ignored.
func devSessionRunning() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	pidPath, err := ExpandHome(DevPidPath)
	if err != nil {
		return false
	}
	rawPid, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(rawPid)))
	if err != nil {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// signal 0 checks the process exists without touching it
	return process.Signal(syscall.Signal(0)) == nil
}
