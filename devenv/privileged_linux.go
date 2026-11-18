package devenv

import (
	"errors"
	"fmt"
	"os"
)

const (
	capNetAdmin    = 12
	ipForwardPath  = "/proc/sys/net/ipv4/ip_forward"
	selfStatusPath = "/proc/self/status"
)

var errNotPrivileged = errors.New("run as root, or in a container started with docker run --privileged")

func checkPrivileged() error {
	status, err := os.Open(selfStatusPath)
	if err != nil {
		return fmt.Errorf("read capabilities: %w", err)
	}
	defer status.Close()

	caps, err := parseCapEff(status)
	if err != nil {
		return fmt.Errorf("read capabilities: %w", err)
	}
	if caps&(1<<capNetAdmin) == 0 {
		return fmt.Errorf("missing CAP_NET_ADMIN: %w", errNotPrivileged)
	}

	forward, err := os.OpenFile(ipForwardPath, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %s for writing: %w", ipForwardPath, errNotPrivileged)
	}
	return forward.Close()
}
