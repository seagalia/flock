//go:build !linux

package nm

import (
	"context"
	"net"

	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/sdk/errors"
)

type nmOthers struct {
}

func NewNetworkManager(conf config.NetworkManagerConfig, addresses MachineIPs) (NetworkManager, error) {
	return &nmOthers{}, nil
}

func (n *nmOthers) SyncWildcards(ctx context.Context) error {
	return nil
}

func (n *nmOthers) RouteGetAll(ip net.IP) ([]*Route, error) {
	return nil, errors.NewErrNotImplemented("RouteGetAll")
}

func (n *nmOthers) AddVip(ipNet *net.IPNet) error {
	return errors.NewErrNotImplemented("AddVip")
}

func (n *nmOthers) DelVip(ipNet *net.IPNet) error {
	return errors.NewErrNotImplemented("DelVip")
}

func (n *nmOthers) AddSnat(ipNet *net.IPNet, port uint16, isUdp bool, logged bool) error {
	return errors.NewErrNotImplemented("AddSnat")
}

func (n *nmOthers) DelSnat(ipNet *net.IPNet, port uint16, isUdp bool, logged bool) error {
	return errors.NewErrNotImplemented("DelSnat")
}

func (n *nmOthers) AddMarkLog(ipNet *net.IPNet, port uint16, isUdp bool) error {
	return errors.NewErrNotImplemented("AddMarkLog")
}

func (n *nmOthers) DelMarkLog(ipNet *net.IPNet, port uint16, isUdp bool) error {
	return errors.NewErrNotImplemented("DelMarkLog")
}

func (n *nmOthers) AddNfLog(ipNet *net.IPNet, port uint16, prefix string, isUdp bool) error {
	return errors.NewErrNotImplemented("AddNfLog")
}

func (n *nmOthers) DelNfLog(ipNet *net.IPNet, port uint16, prefix string, isUdp bool) error {
	return errors.NewErrNotImplemented("DelNfLog")
}

func (n *nmOthers) AddConnLimit(ipNet *net.IPNet, port uint16, isUdp bool, limit uint64, withSrcAddr bool, withDestAddr bool) error {
	return errors.NewErrNotImplemented("AddConnLimit")
}

func (n *nmOthers) DelConnLimit(ipNet *net.IPNet, port uint16, isUdp bool, limit uint64, withSrcAddr bool, withDestAddr bool) error {
	return errors.NewErrNotImplemented("DelConnLimit")
}

func (n *nmOthers) AddConnRateLimit(ipNet *net.IPNet, port uint16, isUdp bool, rate string, burst uint64, withSrcAddr bool, withDestAddr bool) error {
	return errors.NewErrNotImplemented("AddConnRateLimit")
}

func (n *nmOthers) DelConnRateLimit(ipNet *net.IPNet, port uint16, isUdp bool, rate string, burst uint64, withSrcAddr bool, withDestAddr bool) error {
	return errors.NewErrNotImplemented("DelConnRateLimit")
}
