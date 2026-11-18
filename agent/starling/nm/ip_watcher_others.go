//go:build !linux

package nm

import (
	"context"
	"net"
)

// IPWatcher has no address to follow outside linux, a wildcard VIP expands to nothing.
type IPWatcher struct {
	changed chan struct{}
}

func NewIPWatcher(dummyInterfaceName string) (*IPWatcher, error) {
	return &IPWatcher{changed: make(chan struct{}, 1)}, nil
}

func (s *IPWatcher) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (s *IPWatcher) Current(family int) []net.IP {
	return nil
}

func (s *IPWatcher) Changed() <-chan struct{} {
	return s.changed
}
