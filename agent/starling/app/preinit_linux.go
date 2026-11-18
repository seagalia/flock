//go:build linux

package app

import (
	"fmt"
	"maps"

	"github.com/hashicorp/go-multierror"
	"github.com/lorenzosaino/go-sysctl"
	"github.com/pbnjay/memory"
	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/sdk/logging"
)

const (
	lowerBucket             = 65536
	maxBucket               = 262144
	defaultFileMax          = 5847600
	defaultTcpMaxSynBacklog = 30000
	defaultSomaxconn        = 8096
	defaultTcpMaxTwBuckets  = 262144
	defaultNetDevMaxBacklog = 5000
	defaultRmemMax          = 8388608
	defaultWmemMax          = 8388608
	defaultOptemMax         = 25165824
)

type SysctlRequirements struct {
	IPv4Forwarding   bool
	IPv6Forwarding   bool
	IPVSConntrack    bool
	StrictARP        bool
	IPv4NonlocalBind bool
	IPv6NonlocalBind bool
}

func (a *App) preInitOS() error {
	if a.cnf.DevMode || a.cnf.System.SysctlMode == config.SysctlModeDisabled {
		return nil
	}
	if a.cnf.System.SysctlMode == config.SysctlModeValidate {
		return a.preInitValidateSysctls(SysctlRequirements{
			IPv4Forwarding:   true,
			IPv6Forwarding:   true,
			IPVSConntrack:    a.cnf.AdvertisementConfig != nil && !a.cnf.AdvertisementConfig.DisableSnat,
			StrictARP:        true,
			IPv4NonlocalBind: true,
			IPv6NonlocalBind: true,
		})
	}
	return a.preInitSystemConfig()
}

func (a *App) preInitSystemConfig() error {
	a.entry.Info().Msg("Running pre init OS ...")
	defer a.entry.Info().Msg("Pre init OS done.")
	ctMax, ctBuckets := conntrackTuning()

	opts := map[string]string{
		// allow ip forwarding, understand act like a router
		"net.ipv4.ip_forward":          "1",
		"net.ipv6.conf.all.forwarding": "1",
		// ensure realservers doesn't get a redirect to forward packets directly to the client
		"net.ipv4.conf.all.accept_redirects": "0",
		// allow to bind on ip not owned by the interface
		"net.ipv4.ip_nonlocal_bind": "1",
		"net.ipv6.ip_nonlocal_bind": "1",
		// Schedule non-SYN packets
		"net.ipv4.vs.sloppy_tcp": "1",
		// see http://kb.linuxvirtualserver.org/wiki/Using_arp_announce/arp_ignore_to_disable_ARP
		// reply only if the target IP address is local address
		// configured on the incoming interface
		// this will prevent the kernel to reply to ARP request on local route created without ip associated
		"net.ipv4.conf.all.arp_ignore": "1",
		// Always use the best local address for this target
		"net.ipv4.conf.all.arp_announce": "2",
		// Ensure disable conntrack if snat is disabled
		"net.ipv4.vs.conntrack": "0",
		// set conntrack limit using correct formula. It can't be lower than 65536 buckets
		// and bigger than 262144 buckets and conntrack max is equivalent to buckets.
		// see https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git/tree/Documentation/networking/nf_conntrack-sysctl.rst?h=v5.15.158
		"net.netfilter.nf_conntrack_max":     fmt.Sprintf("%d", ctMax),
		"net.netfilter.nf_conntrack_buckets": fmt.Sprintf("%d", ctBuckets),
		// Increase size of file handles and inode cache
		"fs.file-max": fmt.Sprintf("%d", defaultFileMax),
		// Maximal number of remembered connection requests, which have not received an acknowledgment from connecting client.
		"net.ipv4.tcp_max_syn_backlog": fmt.Sprintf("%d", defaultTcpMaxSynBacklog),
		// Raising SOMAXCONN lowers chance of the port being unavailable under
		// even small SYNFLOOD attack, and reduces possibilities of side channel
		// vulnerabilities.
		"net.core.somaxconn": fmt.Sprintf("%d", defaultSomaxconn),
		// Packets queued on input when the interface receives them faster than the kernel processes them
		"net.core.netdev_max_backlog": fmt.Sprintf("%d", defaultNetDevMaxBacklog),
		// Enable SYN cookies protection against SYN flood attacks
		"net.ipv4.tcp_syncookies":     "1",
		"net.ipv4.tcp_window_scaling": "1",
		// Increase the maximum memory used to store TCP packets
		"net.core.rmem_max": fmt.Sprintf("%d", defaultRmemMax),
		// Increase the maximum memory used to store TCP packets
		"net.core.wmem_max": fmt.Sprintf("%d", defaultWmemMax),
		// use bbr congestion control for better performance
		// see here: https://djangocas.dev/blog/huge-improve-network-performance-by-change-tcp-congestion-control-to-bbr/
		// run `modprobe tcp_bbr && echo "tcp_bbr" > /etc/modules-load.d/bbr.conf` to enable it before

		// net.ipv4.tcp_congestion_control = bbr
		// net.core.default_qdisc = fq

		// Increase the tcp-time-wait buckets pool size to prevent simple DOS attacks
		"net.ipv4.tcp_max_tw_buckets": fmt.Sprintf("%d", defaultTcpMaxTwBuckets),

		// set a bigger range of port to be used by TCP and UDP traffic to choose the local port.
		"net.ipv4.ip_local_port_range": "5000 60999",
		"net.core.optmem_max":          fmt.Sprintf("%d", defaultOptemMax),
	}

	// If snat not disabled we enable conntrack tracking for be able to snat the packets to the realserver
	// we disable it if snat is disabled because it's not needed, and it's a performance hit
	if a.cnf.AdvertisementConfig != nil && !a.cnf.AdvertisementConfig.DisableSnat {
		opts["net.ipv4.vs.conntrack"] = "1"
	}

	maps.Copy(opts, a.cnf.System.ExtraSysctl)
	return sysctlSet(a.cnf.System.SysctlMode == config.SysctlModePermissive, opts)
}

func conntrackTuning() (ctMax uint64, buckets uint64) {
	// see reference from: https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git/tree/Documentation/networking/nf_conntrack-sysctl.rst?h=v5.15.158
	buckets = min(max(memory.TotalMemory()/16384, lowerBucket), maxBucket)
	ctMax = buckets
	return
}

// sysctlSetter is replaced in tests
var sysctlSetter = sysctl.Set

// tolerantSysctls are host global: missing or refused inside a container, where failing on them is only a warning
var tolerantSysctls = map[string]bool{
	"fs.file-max":                        true,
	"net.netfilter.nf_conntrack_buckets": true,
	"net.netfilter.nf_conntrack_max":     true,
	"net.core.rmem_max":                  true,
	"net.core.wmem_max":                  true,
	"net.core.netdev_max_backlog":        true,
}

func sysctlSet(isPermissive bool, toSet map[string]string) error {
	entry := logging.Component("sysctl")
	var result error
	for k, v := range toSet {
		err := sysctlSetter(k, v)
		isTolerated := isPermissive && tolerantSysctls[k]
		if err != nil && isTolerated {
			entry.Warn().Str(k, v).Err(err).Msg("failed to set global sysctl value, ignoring")
			continue
		}
		if err != nil {
			result = multierror.Append(
				result,
				fmt.Errorf("failed to set sysctl %s to %s: %w", k, v, err),
			)
		}
		entry.Info().Str(k, v).Msg("set sysctl value")
	}
	return result
}

func (a *App) preInitValidateSysctls(
	req SysctlRequirements,
) error {
	a.entry.Debug().Msg("Validating sysctl prerequisites ...")

	type requirement struct {
		key    string
		want   string
		reason string
	}

	var checks []requirement

	add := func(enabled bool, key string, want string, reason string) {
		if enabled {
			checks = append(checks, requirement{
				key:    key,
				want:   want,
				reason: reason,
			})
		}
	}

	add(req.IPv4Forwarding,
		"net.ipv4.ip_forward", "1",
		"the dataplane requires IPv4 forwarding",
	)

	add(req.IPv6Forwarding,
		"net.ipv6.conf.all.forwarding", "1",
		"the dataplane requires IPv6 forwarding",
	)

	add(req.IPVSConntrack,
		"net.ipv4.vs.conntrack", "1",
		"the dataplane requires IPVS conntrack integration",
	)

	add(req.StrictARP,
		"net.ipv4.conf.all.arp_ignore", "1",
		"the VIP strategy requires strict ARP replies",
	)

	add(req.StrictARP,
		"net.ipv4.conf.all.arp_announce", "2",
		"the VIP strategy requires strict ARP source address selection",
	)

	add(req.IPv4NonlocalBind,
		"net.ipv4.ip_nonlocal_bind", "1",
		"IPv4 listeners require nonlocal binding",
	)

	add(req.IPv6NonlocalBind,
		"net.ipv6.ip_nonlocal_bind", "1",
		"IPv6 listeners require nonlocal binding",
	)

	var result error

	for _, check := range checks {
		entry := a.entry.With().
			Str("sysctl", check.key).
			Str("required", check.want).
			Str("reason", check.reason).
			Logger()

		val, err := sysctl.Get(check.key)
		if err != nil {
			entry.Error().Err(err).Msg("Failed to get sysctl value")
			result = multierror.Append(result, err)
			continue
		}
		if val != check.want {
			entry.Error().
				Str("actual", val).
				Msg("Sysctl prerequisite not satisfied")

			result = multierror.Append(result, fmt.Errorf(
				"%s=%s, required %s: %s",
				check.key, val, check.want, check.reason,
			))
			continue
		}

		entry.Debug().
			Str("actual", val).
			Msg("Sysctl prerequisite satisfied")
	}

	if result == nil {
		a.entry.Info().
			Int("checks", len(checks)).
			Msg("Sysctl validation completed successfully")
	}

	return result
}
