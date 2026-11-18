package nm

import (
	"context"
	"net"

	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/logging"
)

type LogNetworkManager struct {
	entry zerolog.Logger
}

func NewLogNetworkManager() *LogNetworkManager {
	return &LogNetworkManager{
		entry: logging.Component("log_network_manager"),
	}
}

func (n *LogNetworkManager) RouteGetAll(ip net.IP) ([]*Route, error) {
	n.entry.Info().Stringer("ip", ip).Msg("RouteGetAll")
	return nil, nil
}

func (n *LogNetworkManager) SyncWildcards(ctx context.Context) error {
	return nil
}

func (n *LogNetworkManager) AddVip(ipNet *net.IPNet) error {
	n.entry.Info().Stringer("ipNet", ipNet).Msg("AddVip")
	return nil
}

func (n *LogNetworkManager) DelVip(ipNet *net.IPNet) error {
	n.entry.Info().Stringer("ipNet", ipNet).Msg("DelVip")
	return nil
}

func (n *LogNetworkManager) AddSnat(ipNet *net.IPNet, port uint16, isUdp bool, logged bool) error {
	n.entry.Info().
		Fields(map[string]any{
			"ipNet":  ipNet,
			"port":   port,
			"isUdp":  isUdp,
			"logged": logged,
		}).
		Msg("AddSnat")
	return nil
}

func (n *LogNetworkManager) AddNfLog(ipNet *net.IPNet, port uint16, prefix string, isUdp bool) error {
	n.entry.Info().
		Fields(map[string]any{
			"ipNet":  ipNet,
			"port":   port,
			"prefix": prefix,
			"isUdp":  isUdp,
		}).
		Msg("AddNfLog")
	return nil
}

func (n *LogNetworkManager) DelNfLog(ipNet *net.IPNet, port uint16, prefix string, isUdp bool) error {
	n.entry.Info().
		Fields(map[string]any{
			"ipNet":  ipNet,
			"port":   port,
			"prefix": prefix,
			"isUdp":  isUdp,
		}).
		Msg("DelNfLog")
	return nil
}

func (n *LogNetworkManager) AddMarkLog(ipNet *net.IPNet, port uint16, isUdp bool) error {
	n.entry.Info().
		Fields(map[string]any{
			"ipNet": ipNet,
			"port":  port,
			"isUdp": isUdp,
		}).
		Msg("AddMarkLog")
	return nil
}

func (n *LogNetworkManager) DelMarkLog(ipNet *net.IPNet, port uint16, isUdp bool) error {
	n.entry.Info().
		Fields(map[string]any{
			"ipNet": ipNet,
			"port":  port,
			"isUdp": isUdp,
		}).
		Msg("AddMarkLog")
	return nil
}

func (n *LogNetworkManager) DelSnat(ipNet *net.IPNet, port uint16, isUdp bool, logged bool) error {
	n.entry.Info().
		Fields(map[string]any{
			"ipNet":  ipNet,
			"port":   port,
			"isUdp":  isUdp,
			"logged": logged,
		}).
		Msg("DelSnat")
	return nil
}

func (n *LogNetworkManager) AddConnLimit(ipNet *net.IPNet, port uint16, isUdp bool, limit uint64, withSrcAddr bool, withDestAddr bool) error {
	n.entry.Info().
		Fields(map[string]any{
			"ipNet":        ipNet,
			"port":         port,
			"isUdp":        isUdp,
			"limit":        limit,
			"withSrcAddr":  withSrcAddr,
			"withDestAddr": withDestAddr,
		}).
		Msg("AddConnLimit")
	return nil
}

func (n *LogNetworkManager) DelConnLimit(ipNet *net.IPNet, port uint16, isUdp bool, limit uint64, withSrcAddr bool, withDestAddr bool) error {
	n.entry.Info().
		Fields(map[string]any{
			"ipNet":        ipNet,
			"port":         port,
			"isUdp":        isUdp,
			"limit":        limit,
			"withSrcAddr":  withSrcAddr,
			"withDestAddr": withDestAddr,
		}).
		Msg("DelConnLimit")
	return nil
}

func (n *LogNetworkManager) AddConnRateLimit(ipNet *net.IPNet, port uint16, isUdp bool, rate string, burst uint64, withSrcAddr bool, withDestAddr bool) error {
	n.entry.Info().
		Fields(map[string]any{
			"ipNet":        ipNet,
			"port":         port,
			"isUdp":        isUdp,
			"rate":         rate,
			"burst":        burst,
			"withSrcAddr":  withSrcAddr,
			"withDestAddr": withDestAddr,
		}).
		Msg("AddConnRateLimit")
	return nil

}

func (n *LogNetworkManager) DelConnRateLimit(ipNet *net.IPNet, port uint16, isUdp bool, rate string, burst uint64, withSrcAddr bool, withDestAddr bool) error {
	n.entry.Info().
		Fields(map[string]any{
			"ipNet":        ipNet,
			"port":         port,
			"isUdp":        isUdp,
			"rate":         rate,
			"burst":        burst,
			"withSrcAddr":  withSrcAddr,
			"withDestAddr": withDestAddr,
		}).
		Msg("DelConnRateLimit")
	return nil
}
