//go:build linux

package nm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/jsimonetti/rtnetlink/v2"
	"github.com/mdlayher/netlink"
	"github.com/seagalia/flock/sdk/logging"
	"golang.org/x/sys/unix"
)

// IPWatcher follows the addresses of the machine and gives the expansion of the wildcard VIPs.
type IPWatcher struct {
	dummyName string
	// events only wakes the watcher up, each wake-up lists the addresses again since an event can repeat.
	events *rtnetlink.Conn
	query  *rtnetlink.Conn
	// changed holds at most one pending signal, a burst of events gives one signal per real change of the expansion.
	changed chan struct{}

	mu   sync.Mutex
	ipv4 []net.IP
	ipv6 []net.IP
}

// NewIPWatcher subscribes to the address events before listing the addresses, so no change falls between the
// two. The caller must Run it, which closes the connections.
func NewIPWatcher(dummyInterfaceName string) (*IPWatcher, error) {
	events, err := rtnetlink.Dial(&netlink.Config{Groups: unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR})
	if err != nil {
		return nil, fmt.Errorf("subscribe to the address events: %w", err)
	}
	query, err := rtnetlink.Dial(nil)
	if err != nil {
		_ = events.Close()
		return nil, fmt.Errorf("connect to rtnetlink: %w", err)
	}
	s := &IPWatcher{
		dummyName: dummyInterfaceName,
		events:    events,
		query:     query,
		changed:   make(chan struct{}, 1),
	}
	if _, err := s.refresh(); err != nil {
		_ = events.Close()
		_ = query.Close()
		return nil, err
	}
	return s, nil
}

// Run returns nil once ctx is done and an error when the subscription breaks.
func (s *IPWatcher) Run(ctx context.Context) error {
	defer s.query.Close()
	defer s.events.Close()
	// Receive only ends when the connection is closed
	stop := context.AfterFunc(ctx, func() { _ = s.events.Close() })
	defer stop()

	for {
		_, _, err := s.events.Receive()
		if ctx.Err() != nil {
			return nil
		}
		// the kernel dropped events, the listing catches up on them
		overflowed := errors.Is(err, unix.ENOBUFS)
		if err != nil && !overflowed {
			return fmt.Errorf("receive the address events: %w", err)
		}
		changed := s.refreshWithRetry(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if changed {
			s.signal()
		}
	}
}

// refreshWithRetry keeps the subscription open while the listing fails, losing it would end the tracking for the rest
// of the agent's life. It gives up only when ctx is done.
func (s *IPWatcher) refreshWithRetry(ctx context.Context) bool {
	entry := logging.Component("ip_watcher")
	wait := retryMinWait
	for {
		changed, err := s.refresh()
		if err == nil {
			return changed
		}
		entry.Warn().Err(err).Dur("retry_in", wait).Msg("cannot list the addresses of the machine, retrying")
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
		wait = nextRetryWait(wait)
	}
}

func (s *IPWatcher) Current(family int) []net.IP {
	s.mu.Lock()
	defer s.mu.Unlock()
	if family == unix.AF_INET {
		return append([]net.IP(nil), s.ipv4...)
	}
	return append([]net.IP(nil), s.ipv6...)
}

// Changed signals that the expansion differs from the previous one.
func (s *IPWatcher) Changed() <-chan struct{} {
	return s.changed
}

func (s *IPWatcher) signal() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

func (s *IPWatcher) refresh() (bool, error) {
	msgs, err := s.query.Address.List()
	if err != nil {
		return false, fmt.Errorf("list the addresses of the machine: %w", err)
	}
	var dummyIndex uint32
	if iface, err := net.InterfaceByName(s.dummyName); err == nil {
		dummyIndex = uint32(iface.Index)
	}
	addrs := make([]machineAddr, 0, len(msgs))
	for _, m := range msgs {
		if m.Attributes == nil {
			continue
		}
		// Local is the address of the interface, Address the peer's on a point-to-point link
		ip := m.Attributes.Local
		if ip == nil {
			ip = m.Attributes.Address
		}
		addrs = append(addrs, machineAddr{ifaceIndex: m.Index, ip: ip})
	}
	ipv4 := expandWildcard(addrs, unix.AF_INET, dummyIndex)
	ipv6 := expandWildcard(addrs, unix.AF_INET6, dummyIndex)

	s.mu.Lock()
	defer s.mu.Unlock()
	changed := !sameIPs(s.ipv4, ipv4) || !sameIPs(s.ipv6, ipv6)
	s.ipv4, s.ipv6 = ipv4, ipv6
	return changed, nil
}
