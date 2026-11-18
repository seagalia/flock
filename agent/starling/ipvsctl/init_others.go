//go:build !linux

package ipvsctl

import (
	"fmt"
	"runtime"
)

var (
	errUnimplemented = fmt.Errorf("ipvs is not implemented on %s/%s",
		runtime.GOOS, runtime.GOARCH)
)

func init() {
	client = &errClient{err: errUnimplemented}
}

// Init is a no-op, ipvs stays unimplemented outside linux
func Init() error {
	return nil
}
