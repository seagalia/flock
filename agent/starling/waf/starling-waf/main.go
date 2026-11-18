package main

import (
	"fmt"

	"github.com/KimMachineGun/automemlimit/memlimit"
	"github.com/alecthomas/kong"
	"github.com/rs/zerolog/log"
	"github.com/seagalia/flock/agent/starling/procmemory"
	"github.com/seagalia/flock/agent/starling/waf/app"
	"github.com/seagalia/flock/sdk/logging"
)

var (
	version = "0.0.1-dev"
	commit  = "none"
	date    = "unknown"
)

type CLI struct {
	Version           kong.VersionFlag `help:"Show version."`
	SocketPath        string           `short:"s" default:"./waf.sock" help:"Unix socket serving envoy's ext_proc filter and the agent's registration of WAF configs."`
	MetricsSocketPath string           `short:"m" default:"./waf-metrics.sock" help:"Unix socket serving the Prometheus metrics of the WAF on /metrics."`
	LogLevel          string           `short:"l" default:"info" enum:"trace,debug,info,warn,error" help:"Lowest level of the logged lines: ${enum}."`
	MemoryPercent     uint64           `default:"100" help:"Share in percent of the memory of the cgroup of the WAF (of the host without a limit) it holds requests in, lower when it shares its cgroup with other processes."`
}

func main() {
	var cli CLI
	kong.Parse(&cli,
		kong.Description("WAF of the starling agent: filters envoy's traffic through ext_proc with coraza and the OWASP CRS, and takes the WAF configs the agent registers."),
		kong.Vars{"version": fmt.Sprintf("%s, commit %s, built at %s", version, commit, date)},
	)

	err := logging.Setup(logging.Options{InJson: true, Level: cli.LogLevel})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to set up logging")
	}

	memory := procmemory.Available() * cli.MemoryPercent / 100
	// the Go collector works harder near this limit, before the kernel reclaims or kills at the cgroup's
	_, err = memlimit.Set(memlimit.WithProvider(memlimit.Limit(memory)), memlimit.WithRatio(0.9))

	if err != nil {
		log.Fatal().Err(err).Msg("failed to set the go memory limit")
	}
	wafApp, err := app.NewApp(cli.SocketPath, cli.MetricsSocketPath, memory)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create app")
	}
	err = wafApp.Run()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to serve")
	}
}
