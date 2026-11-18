package envoyctl

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"errors"

	accesslogv3 "github.com/envoyproxy/go-control-plane/envoy/config/accesslog/v3"
	bootstrapv3 "github.com/envoyproxy/go-control-plane/envoy/config/bootstrap/v3"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	overloadv3 "github.com/envoyproxy/go-control-plane/envoy/config/overload/v3"
	cgroup_memoryv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/resource_monitors/cgroup_memory/v3"
	downstream_connectionsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/resource_monitors/downstream_connections/v3"
	fixed_heapv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/resource_monitors/fixed_heap/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/nxadm/tail"
	"github.com/pbnjay/memory"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.yaml.in/yaml/v2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"time"

	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/agent/starling/logextends"
	"github.com/seagalia/flock/agent/starling/memsplit"
	"github.com/seagalia/flock/sdk/logging"
)

const (
	listenPort     uint32 = 19000
	logAppFormat          = `{"thread_id": "%t", "log_level": "%l", "logger_name": "%n", "file": "%g:%#", "message": "%j"}`
	wafClusterName        = "ext_proc_cluster"
	// An arbitrary high bound, only there because envoy's default circuit breaker stops at 1024 requests, fewer than the
	// WAF of a big node holds. The WAF sizes the streams it holds from its own memory and refuses the others.
	wafMaxRequests      = 65536
	defaultStopTimeout  = 2 * time.Minute
	getPidTimeout       = 5 * time.Second
	serviceStartTimeout = time.Minute
	commandWaitDelay    = time.Second
)

type Runner struct {
	config        *config.EnvoyConfig
	serverCtlPort uint16
	wafConfig     *config.WafConfig
	memory        *memsplit.Split
	entry         zerolog.Logger
	stopTimeout   time.Duration
}

func NewRunner(
	envoyConfig *config.EnvoyConfig, serverCtlPort uint16, wafConfig *config.WafConfig, memorySplit *memsplit.Split,
) *Runner {
	return &Runner{
		config:        envoyConfig,
		serverCtlPort: serverCtlPort,
		wafConfig:     wafConfig,
		memory:        memorySplit,
		entry:         logging.Component("envoy"),
		stopTimeout:   defaultStopTimeout,
	}
}

func (r *Runner) makeConfigFile(tempDir string) (string, error) {
	cnf, err := r.generateConfig()
	if err != nil {
		return "", fmt.Errorf("failed to generate envoy config: %w", err)
	}
	file, err := os.CreateTemp(tempDir, "flock-agent-envoy-config-*.json")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer file.Close()
	b, err := protojson.MarshalOptions{
		Multiline: true,
		Indent:    "  ",
	}.Marshal(cnf)
	if err != nil {
		return "", fmt.Errorf("failed to marshal envoy config to json: %w", err)
	}
	if zerolog.GlobalLevel() <= zerolog.DebugLevel {
		var data map[string]any
		_ = json.Unmarshal(b, &data) // nolint
		inYaml, _ := yaml.Marshal(data)
		r.entry.Debug().Msg("Envoy config created: \n" + string(inYaml))
	}

	_, err = file.Write(b)
	if err != nil {
		return "", fmt.Errorf("failed to write envoy config to file: %w", err)
	}
	return file.Name(), nil
}

func (r *Runner) generateConfig() (*bootstrapv3.Bootstrap, error) {
	memoryMonitorName, memoryMonitorConf, err := r.memoryMonitor()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal memory monitor config: %w", err)
	}
	var accessLogConf *anypb.Any
	accessLogName := "envoy.access_loggers.stdout"
	accessLogConf, err = anypb.New(accessLogStdout)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal access log config: %w", err)
	}
	connectionsMonitorConf, err := anypb.New(&downstream_connectionsv3.DownstreamConnectionsConfig{
		MaxActiveDownstreamConnections: int64(r.config.MaxConnections),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal downstream connections monitor config: %w", err)
	}
	transSocket, err := anypb.New(&tlsv3.UpstreamTlsContext{
		CommonTlsContext: &tlsv3.CommonTlsContext{},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal transport socket: %w", err)
	}
	httpProtocolOptions, err := anypb.New(&httpv3.HttpProtocolOptions{
		UpstreamHttpProtocolOptions: &corev3.UpstreamHttpProtocolOptions{
			AutoSni: true,
		},
		UpstreamProtocolOptions: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{
			ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
				ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
					Http2ProtocolOptions: &corev3.Http2ProtocolOptions{
						ConnectionKeepalive: &corev3.KeepaliveSettings{
							Interval: durationpb.New(30 * time.Second),
							Timeout:  durationpb.New(5 * time.Second),
						},
					},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal http protocol options: %w", err)
	}
	wafHttpProtocolOptions, err := anypb.New(&httpv3.HttpProtocolOptions{
		UpstreamProtocolOptions: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{
			ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
				ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
					Http2ProtocolOptions: &corev3.Http2ProtocolOptions{},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal waf http protocol options: %w", err)
	}
	return &bootstrapv3.Bootstrap{
		OverloadManager: &overloadv3.OverloadManager{
			RefreshInterval: durationpb.New(250 * time.Millisecond),
			ResourceMonitors: []*overloadv3.ResourceMonitor{
				{
					Name: memoryMonitorName,
					ConfigType: &overloadv3.ResourceMonitor_TypedConfig{
						TypedConfig: memoryMonitorConf,
					},
				},
				{
					// envoy refuses the connections over the limit when it accepts them, no action needed
					Name: "envoy.resource_monitors.global_downstream_max_connections",
					ConfigType: &overloadv3.ResourceMonitor_TypedConfig{
						TypedConfig: connectionsMonitorConf,
					},
				},
			},
			Actions: []*overloadv3.OverloadAction{
				{
					Name: "envoy.overload_actions.shrink_heap",
					Triggers: []*overloadv3.Trigger{
						{
							Name: memoryMonitorName,
							TriggerOneof: &overloadv3.Trigger_Threshold{
								Threshold: &overloadv3.ThresholdTrigger{Value: 0.95},
							},
						},
					},
				},
				{
					Name: "envoy.overload_actions.stop_accepting_requests",
					Triggers: []*overloadv3.Trigger{
						{
							Name: memoryMonitorName,
							TriggerOneof: &overloadv3.Trigger_Threshold{
								Threshold: &overloadv3.ThresholdTrigger{Value: 0.98},
							},
						},
					},
				},
			},
		},
		Admin: &bootstrapv3.Admin{
			AccessLog: []*accesslogv3.AccessLog{
				{
					Name:       accessLogName,
					ConfigType: &accesslogv3.AccessLog_TypedConfig{TypedConfig: accessLogConf},
				},
			},
			Address: &corev3.Address{
				Address: &corev3.Address_SocketAddress{
					SocketAddress: &corev3.SocketAddress{
						Address: "127.0.0.1",
						PortSpecifier: &corev3.SocketAddress_PortValue{
							PortValue: listenPort,
						},
					},
				},
			},
			IgnoreGlobalConnLimit: false,
		},
		Node: &corev3.Node{
			Id:      "starling-agent",
			Cluster: "flock",
		},
		DynamicResources: &bootstrapv3.Bootstrap_DynamicResources{
			LdsConfig: &corev3.ConfigSource{
				ConfigSourceSpecifier: &corev3.ConfigSource_Ads{},
				ResourceApiVersion:    corev3.ApiVersion_V3,
			},
			CdsConfig: &corev3.ConfigSource{
				ConfigSourceSpecifier: &corev3.ConfigSource_Ads{},
				ResourceApiVersion:    corev3.ApiVersion_V3,
			},
			AdsConfig: &corev3.ApiConfigSource{
				ApiType:             corev3.ApiConfigSource_DELTA_GRPC,
				TransportApiVersion: corev3.ApiVersion_V3,
				GrpcServices: []*corev3.GrpcService{
					{
						TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
							EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{
								ClusterName: "xds_agent",
							},
						},
					},
				},
				SetNodeOnFirstMessageOnly: true,
			},
		},
		StaticResources: &bootstrapv3.Bootstrap_StaticResources{
			Clusters: []*clusterv3.Cluster{
				{
					Name:           "xds_agent",
					ConnectTimeout: durationpb.New(5 * time.Second),
					TransportSocket: &corev3.TransportSocket{
						Name:       "envoy.transport_sockets.tls",
						ConfigType: &corev3.TransportSocket_TypedConfig{TypedConfig: transSocket},
					},
					LoadAssignment: &endpointv3.ClusterLoadAssignment{
						ClusterName: "xds_agent",
						Endpoints: []*endpointv3.LocalityLbEndpoints{
							{
								LbEndpoints: []*endpointv3.LbEndpoint{
									{
										HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
											Endpoint: &endpointv3.Endpoint{
												Address: &corev3.Address{
													Address: &corev3.Address_SocketAddress{
														SocketAddress: &corev3.SocketAddress{
															Address: "127.0.0.1",
															PortSpecifier: &corev3.SocketAddress_PortValue{
																PortValue: uint32(r.serverCtlPort),
															},
														},
													},
												},
											},
										},
									},
								},
							},
						},
					},
					TypedExtensionProtocolOptions: map[string]*anypb.Any{
						"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": httpProtocolOptions,
					},
				},
				{
					Name:                 wafClusterName,
					ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_STATIC},
					ConnectTimeout:       durationpb.New(1 * time.Second),
					// MaxPendingRequests keeps its default because a stream waits as pending while the connection to
					// the WAF opens, and 0 would refuse every request.
					CircuitBreakers: &clusterv3.CircuitBreakers{
						Thresholds: []*clusterv3.CircuitBreakers_Thresholds{
							{MaxRequests: wrapperspb.UInt32(wafMaxRequests)},
						},
					},
					TypedExtensionProtocolOptions: map[string]*anypb.Any{
						"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": wafHttpProtocolOptions,
					},
					LoadAssignment: &endpointv3.ClusterLoadAssignment{
						ClusterName: wafClusterName,
						Endpoints: []*endpointv3.LocalityLbEndpoints{
							{
								LbEndpoints: []*endpointv3.LbEndpoint{
									{
										HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
											Endpoint: &endpointv3.Endpoint{
												Address: &corev3.Address{
													Address: &corev3.Address_Pipe{
														Pipe: &corev3.Pipe{Path: r.wafConfig.SocketPath()},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}, nil
}

// memoryMonitor picks the overload monitor of envoy's memory. Envoy alone in its cgroup, under a service or in its
// leaf of the agent's split cgroup, gets the cgroup_memory monitor, which counts all its memory. Otherwise its cgroup
// counts the agent and the WAF too, so fixed_heap watches envoy's own heap. cgroup_memory fails envoy's startup
// without cgroups, off Linux.
func (r *Runner) memoryMonitor() (string, *anypb.Any, error) {
	isService := r.config.EnvoyService != nil
	if isService && runtime.GOOS == "linux" {
		// the cgroup of the service is not the agent's, the host only caps it
		hostMemory := memory.TotalMemory()
		conf, err := anypb.New(&cgroup_memoryv3.CgroupMemoryConfig{
			MaxMemoryBytes: hostMemory * r.memory.EnvoyPercent / 100,
		})
		return "envoy.resource_monitors.cgroup_memory", conf, err
	}
	if !isService && r.memory.HasCgroups() {
		conf, err := anypb.New(&cgroup_memoryv3.CgroupMemoryConfig{MaxMemoryBytes: r.memory.Envoy})
		return "envoy.resource_monitors.cgroup_memory", conf, err
	}
	conf, err := anypb.New(&fixed_heapv3.FixedHeapConfig{MaxHeapSizeBytes: r.memory.Envoy})
	return "envoy.resource_monitors.fixed_heap", conf, err
}

func (r *Runner) getLogLevel() string {
	if r.config.EnvoyExec.LogLevel != "" {
		return r.config.EnvoyExec.LogLevel
	}
	switch zerolog.GlobalLevel() {
	case zerolog.TraceLevel:
		return "trace"
	case zerolog.DebugLevel:
		return "debug"
	case zerolog.InfoLevel:
		return "info"
	case zerolog.WarnLevel:
		return "warn"
	case zerolog.ErrorLevel:
		return "error"
	case zerolog.FatalLevel:
		return "critical"
	case zerolog.PanicLevel:
		return "critical"
	}
	return "info"
}

func (r *Runner) runAsExec(ctx context.Context) error {
	configFile, err := r.makeConfigFile("")
	if err != nil {
		return fmt.Errorf("failed to make config file: %w", err)
	}
	// envoy gets SIGTERM on ctx cancel and is only killed after stopTimeout
	cmdCtx, cancelFunc := context.WithCancel(context.WithoutCancel(ctx))
	cmd := exec.CommandContext(
		cmdCtx, r.config.EnvoyExec.BinPath,
		"-c", configFile,
		"-l", r.getLogLevel(),
		"--log-format", logAppFormat,
	)
	entry := r.entry.With().Str("source", "exec_envoy").Logger()
	stdout := &logextends.EnvoyLines{Logger: entry, Output: "stdout", AccessLog: true}
	stderr := &logextends.EnvoyLines{Logger: entry, Output: "stderr"}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = r.memory.Start(cmd, memsplit.EnvoyLeaf)
	if err != nil {
		cancelFunc()
		return fmt.Errorf("failed to start envoy: %w", err)
	}
	// done is closed when envoy exits, also when it crashed
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
			r.entry.Warn().Msgf("envoy did not stop after %s, killing", r.stopTimeout)
			cancelFunc()
		}
	}()
	err = cmd.Wait()
	// read before the cancel below: only the stop timer cancels cmdCtx while envoy runs, a kill from outside
	// (OOM) is an error
	killedByStop := cmdCtx.Err() != nil
	stdout.Flush()
	stderr.Flush()
	close(done)
	cancelFunc()
	r.entry.Info().Msg("envoy stopped")
	if err == nil || killedByStop {
		return nil
	}
	return fmt.Errorf("envoy exited with error: %w", err)
}

func (r *Runner) md5File(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (r *Runner) getPid(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, getPidTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		r.config.EnvoyService.GetPidCommand[0],
		r.config.EnvoyService.GetPidCommand[1:]...,
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
	configFile, err := r.makeConfigFile(path.Dir(r.config.EnvoyService.ConfigFilePath))
	if err != nil {
		r.entry.Warn().Msgf("failed to make config file due to error: %s, skipping to use temp directory", err)
		configFile, err = r.makeConfigFile("")
		if err != nil {
			return fmt.Errorf("failed to make config file: %w", err)
		}
	}
	md5Current, _ := r.md5File(r.config.EnvoyService.ConfigFilePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to get md5 of current config file: %w", err)
		}
	}
	md5New, err := r.md5File(configFile)
	if err != nil {
		return fmt.Errorf("failed to get md5 of new config file: %w", err)
	}
	if md5Current != md5New {
		err := os.Chmod(configFile, 0666)
		if err != nil {
			return fmt.Errorf("failed to chmod config file: %w", err)
		}
		err = os.Rename(configFile, r.config.EnvoyService.ConfigFilePath)
		if err != nil {
			return fmt.Errorf("failed to move config file: %w", err)
		}
	} else {
		err := os.Remove(configFile)
		if err != nil {
			return fmt.Errorf("failed to remove config file: %w", err)
		}
	}
	err = r.startService(ctx)
	if err != nil {
		return fmt.Errorf("failed to start service: %w", err)
	}
	entry := r.entry.With().Str("source", "service_envoy").Logger()
	if r.config.EnvoyService.StdoutFilePath != "" {
		closeFunc, err := r.tailFile(
			r.config.EnvoyService.StdoutFilePath,
			&logextends.EnvoyLines{Logger: entry, Output: "stdout", AccessLog: true},
		)
		if err != nil {
			return fmt.Errorf("failed to tee stdout: %w", err)
		}
		defer closeFunc() // nolint:errcheck
	}

	if r.config.EnvoyService.StderrFilePath != "" {
		closeFunc, err := r.tailFile(r.config.EnvoyService.StderrFilePath, &logextends.EnvoyLines{Logger: entry, Output: "stderr"})
		if err != nil {
			return fmt.Errorf("failed to tee stdout: %w", err)
		}
		defer closeFunc() // nolint:errcheck
	}

	<-ctx.Done()
	r.entry.Info().Msg("Stopping envoy service ...")
	r.entry.Info().Msg("Finished stopping envoy service.")
	return nil

}

func (r *Runner) tailFile(filePath string, lines *logextends.EnvoyLines) (func() error, error) {
	r.entry.Debug().Msg("tail file " + filePath)
	t, err := tail.TailFile(filePath, tail.Config{
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

func (r *Runner) IsEnvoyAlive() error {
	if r.config.EnvoyExec != nil {
		return nil
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

func (r *Runner) startService(ctx context.Context) error {
	log.Debug().Msg("starting envoy service ...")
	ctx, cancel := context.WithTimeout(ctx, serviceStartTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		r.config.EnvoyService.StartCommand[0],
		r.config.EnvoyService.StartCommand[1:]...,
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
	log.Debug().Msg("envoy service started")
	return nil
}

func (r *Runner) Run(ctx context.Context) error {
	if r.config.EnvoyExec != nil {
		return r.runAsExec(ctx)
	}
	return r.runAsService(ctx)
}
