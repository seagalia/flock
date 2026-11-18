//go:build linux

package ipvsctl

import (
	"errors"
	"fmt"
	"github.com/cloudflare/ipvs"
	"github.com/pmorjan/kmod"
)

func init() {
	// real client is created by Init(), so importing the package (e.g. in tests) never touches the kernel
	client = &errClient{err: errors.New("ipvs client is not initialized, ipvsctl.Init() must be called first")}
}

func Init() error {
	err := ensureIpvsLoaded("/sys/module", func() error {
		k, err := kmod.New()
		if err != nil {
			return fmt.Errorf("failed to create kmod client: %w", err)
		}
		return k.Load(ipvsModule, "", 0)
	})
	if err != nil {
		return err
	}
	ipvsClient, err := ipvs.New()
	if err != nil {
		return fmt.Errorf("failed to create ipvs client: %w", err)
	}
	client = ipvsClient
	return nil
}
