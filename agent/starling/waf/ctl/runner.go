package ctl

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"errors"

	"github.com/nxadm/tail"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	wafapp "github.com/seagalia/flock/agent/starling/waf/app"

	"time"

	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/agent/starling/logextends"
	"github.com/seagalia/flock/agent/starling/memsplit"
	"github.com/seagalia/flock/sdk/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const (
	// inside the outer stop budget of the agent, so a stuck starling-waf is killed before the agent gives up
	defaultStopTimeout = 2 * time.Minute
	// the status of the agent runs the get-pid command, a hung systemctl must not hang it
	getPidTimeout       = 5 * time.Second
	serviceStartTimeout = time.Minute
	// a killed shell may leave children holding its output pipes open, Wait gives up on them after this delay
	commandWaitDelay   = time.Second
	healthCheckTimeout = 2 * time.Second
)

type Runner struct {
	config      *config.WafConfig
	memory      *memsplit.Split
	entry       zerolog.Logger
	stopTimeout time.Duration
}

func NewRunner(wafConfig *config.WafConfig, memory *memsplit.Split) *Runner {
	return &Runner{
		config:      wafConfig,
		memory:      memory,
		entry:       logging.Component("waf_runner"),
		stopTimeout: defaultStopTimeout,
	}
}

func (r *Runner) getLogLevel() string {
	if r.config.WafExec.LogLevel != "" {
		return r.config.WafExec.LogLevel
	}
	return zerolog.GlobalLevel().String()
}

func (r *Runner) runAsExec(ctx context.Context) error {
	// in its own cgroup, starling-waf reads its share as the limit of the cgroup
	memoryPercent := config.WafMemoryPercent
	if r.memory.HasCgroups() {
		memoryPercent = 100
	}
	// starling-waf gets SIGTERM on ctx cancel and is only killed after stopTimeout
	cmdCtx, cancelFunc := context.WithCancel(context.WithoutCancel(ctx))
	cmd := exec.CommandContext(
		cmdCtx, r.config.WafExec.BinPath,
		"-l", r.getLogLevel(),
		"-s", config.DefaultSocketPath,
		"-m", config.DefaultMetricsSocketPath,
		"--memory-percent", strconv.Itoa(memoryPercent),
	)
	entry := r.entry.With().Str("source", "exec_starling_waf").Logger()
	stdout := &logextends.EnvoyLines{Logger: entry, Output: "stdout", AccessLog: true}
	stderr := &logextends.EnvoyLines{Logger: entry, Output: "stderr"}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := r.memory.Start(cmd, memsplit.WafLeaf)
	if err != nil {
		cancelFunc()
		return fmt.Errorf("failed to start starling-waf: %w", err)
	}

	// done is closed when starling-waf exits, also when it crashed
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		_ = cmd.Process.Signal(syscall.SIGTERM) // nolint
		ticker := time.NewTicker(r.stopTimeout)
		defer ticker.Stop()
		select {
		case <-done:
			return
		case <-ticker.C:
			r.entry.Warn().Msgf("starling-waf did not stop after %s, killing", r.stopTimeout)
			cancelFunc()
		}
	}()
	err = cmd.Wait()
	// read before the cancel below: only the stop timer cancels cmdCtx while starling-waf runs, a kill from outside
	// (OOM) is an error
	killedByStop := cmdCtx.Err() != nil
	stdout.Flush()
	stderr.Flush()
	close(done)
	cancelFunc()
	r.entry.Info().Msg("starling-waf stopped")
	if err == nil || killedByStop {
		return nil
	}
	return fmt.Errorf("starling-waf exited with error: %w", err)
}

func (r *Runner) getPid(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, getPidTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		r.config.WafService.GetPidCommand[0],
		r.config.WafService.GetPidCommand[1:]...,
	)
	cmd.WaitDelay = commandWaitDelay
	r.entry.Debug().Str("command", strings.Join(cmd.Args, " ")).Msg("running command")
	result, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("failed to get pid: %w", err)
	}
	result = bytes.TrimSpace(result)
	pid, err := strconv.Atoi(string(result))
	if err != nil {
		return 0, err
	}
	if pid == 0 {
		return 0, errors.New("pid is 0")
	}
	return pid, nil
}

func (r *Runner) runAsService(ctx context.Context) error {
	err := r.startService(ctx)
	if err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}
	entry := r.entry.With().Str("source", "service_starling_waf").Logger()
	if r.config.WafService.StdoutFilePath != "" {
		closeFunc, err := r.tailFile(
			r.config.WafService.StdoutFilePath,
			&logextends.EnvoyLines{Logger: entry, Output: "stdout", AccessLog: true},
		)
		if err != nil {
			return fmt.Errorf("failed to tee stdout: %w", err)
		}
		defer closeFunc() // nolint:errcheck
	}

	if r.config.WafService.StderrFilePath != "" {
		closeFunc, err := r.tailFile(r.config.WafService.StderrFilePath, &logextends.EnvoyLines{Logger: entry, Output: "stderr"})
		if err != nil {
			return fmt.Errorf("failed to tee stdout: %w", err)
		}
		defer closeFunc() // nolint:errcheck
	}

	<-ctx.Done()
	r.entry.Info().Msg("Stopping starling-waf service ...")
	r.entry.Info().Msg("Finished stopping starling-waf service.")
	return nil

}

func (r *Runner) tailFile(path string, lines *logextends.EnvoyLines) (func() error, error) {
	r.entry.Debug().Msg("tail file " + path)
	t, err := tail.TailFile(path, tail.Config{
		Location: &tail.SeekInfo{
			Offset: 0,
			Whence: io.SeekEnd,
		},
		Follow: true,
		ReOpen: true,
	})
	if err != nil {
		return nil, err
	}
	go func() {
		for line := range t.Lines {
			lines.Emit(line.Text)
		}
	}()
	return t.Stop, nil
}

func (r *Runner) IsAlive() error {
	if r.config.WafEmbedded {
		return nil
	}
	if r.config.WafExec != nil {
		return nil
	}
	if r.config.WafExternal != nil {
		return checkHealth(r.config.WafExternal.SocketPath)
	}
	// the status service calls it with no ctx, getPid bounds the command itself
	pid, err := r.getPid(context.Background())
	if err != nil {
		return err
	}
	_, err = os.FindProcess(pid)
	if err != nil {
		return err
	}
	return nil
}

// checkHealth calls the gRPC health service of the WAF, the only sign of life of a WAF in another container.
func checkHealth(socketPath string) error {
	conn, err := grpc.NewClient("unix:"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("connect to starling-waf: %w", err)
	}
	defer conn.Close() // nolint:errcheck
	ctx, cancel := context.WithTimeout(context.Background(), healthCheckTimeout)
	defer cancel()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("starling-waf health check: %w", err)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("starling-waf is %s", resp.GetStatus())
	}
	return nil
}

func (r *Runner) startService(ctx context.Context) error {
	log.Debug().Msg("starting starling-waf service ...")
	ctx, cancel := context.WithTimeout(ctx, serviceStartTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		r.config.WafService.StartCommand[0],
		r.config.WafService.StartCommand[1:]...,
	)
	cmd.WaitDelay = commandWaitDelay
	entry := r.entry.With().Str("command", strings.Join(cmd.Args, " ")).Logger()
	stdout := &logextends.EnvoyLines{Logger: entry, Output: "stdout"}
	stderr := &logextends.EnvoyLines{Logger: entry, Output: "stderr"}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	stdout.Flush()
	stderr.Flush()
	if err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}
	log.Debug().Msg("starling-waf service started")
	return nil
}

func (r *Runner) runAsEmbedded(ctx context.Context) error {
	app, err := wafapp.NewApp(config.DefaultSocketPath, "", r.memory.Waf)
	if err != nil {
		return fmt.Errorf("failed to create app: %w", err)
	}
	return app.RunWithContext(ctx)
}

func (r *Runner) Run(ctx context.Context) error {
	if r.config.WafEmbedded {
		return r.runAsEmbedded(ctx)
	}
	if r.config.WafExec != nil {
		return r.runAsExec(ctx)
	}
	if r.config.WafExternal != nil {
		<-ctx.Done()
		return nil
	}
	return r.runAsService(ctx)
}
