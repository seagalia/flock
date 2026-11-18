//go:build !linux

package memsplit

import (
	"errors"
	"os/exec"
)

func (s *Split) splitCgroup(_ uint64, _ uint64) (uint64, error) {
	return 0, errors.New("cgroups only exist on Linux")
}

func (s *Split) startInLeaf(cmd *exec.Cmd, _ Leaf) error {
	return cmd.Start()
}
