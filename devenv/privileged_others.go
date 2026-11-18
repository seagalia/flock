//go:build !linux

package devenv

import "errors"

func checkPrivileged() error {
	return errors.New("--privileged needs linux")
}
