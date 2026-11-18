package envoyctl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	bootstrapv3 "github.com/envoyproxy/go-control-plane/envoy/config/bootstrap/v3"
	cgroup_memoryv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/resource_monitors/cgroup_memory/v3"
	downstream_connectionsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/resource_monitors/downstream_connections/v3"
	fixed_heapv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/resource_monitors/fixed_heap/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pbnjay/memory"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/agent/starling/memsplit"
)

// writeFakeEnvoy records its args in argsFile then runs body
func writeFakeEnvoy(dir, argsFile, body string) string {
	GinkgoHelper()
	script := filepath.Join(dir, "fake-envoy")
	content := fmt.Sprintf("#!/bin/sh\necho \"$@\" > %s\n%s\n", argsFile, body)
	Expect(os.WriteFile(script, []byte(content), 0755)).To(Succeed())
	return script
}

var _ = Describe("Runner", func() {
	var (
		tmpDir string
		runner *Runner
	)

	BeforeEach(func() {
		tmpDir = GinkgoT().TempDir()
		runner = NewRunner(
			&config.EnvoyConfig{EnvoyExec: &config.EnvoyExec{BinPath: "envoy"}, MaxConnections: 70000},
			18000,
			&config.WafConfig{WafEmbedded: true},
			memsplit.New(&config.WafConfig{WafEmbedded: true}, true),
		)
	})

	Context("generateConfig", func() {
		It("should generate a valid envoy bootstrap pointing to the agent control plane", func() {
			bootstrap, err := runner.generateConfig()
			Expect(err).ToNot(HaveOccurred())
			expectValidEnvoyConfig(bootstrap)

			Expect(bootstrap.GetAdmin().GetAddress().GetSocketAddress().GetAddress()).To(Equal("127.0.0.1"))
			Expect(bootstrap.GetAdmin().GetAddress().GetSocketAddress().GetPortValue()).To(Equal(listenPort))
			Expect(bootstrap.GetDynamicResources().GetAdsConfig().GetGrpcServices()[0].GetEnvoyGrpc().GetClusterName()).To(Equal("xds_agent"))

			xdsCluster := bootstrap.GetStaticResources().GetClusters()[0]
			Expect(xdsCluster.GetName()).To(Equal("xds_agent"))
			sockAddr := xdsCluster.GetLoadAssignment().GetEndpoints()[0].GetLbEndpoints()[0].GetEndpoint().GetAddress().GetSocketAddress()
			Expect(sockAddr.GetAddress()).To(Equal("127.0.0.1"))
			Expect(sockAddr.GetPortValue()).To(Equal(uint32(18000)))

			wafCluster := bootstrap.GetStaticResources().GetClusters()[1]
			Expect(wafCluster.GetName()).To(Equal("ext_proc_cluster"))
			pipe := wafCluster.GetLoadAssignment().GetEndpoints()[0].GetLbEndpoints()[0].GetEndpoint().GetAddress().GetPipe()
			Expect(pipe.GetPath()).To(Equal("/tmp/starling-waf.sock"))
			Expect(wafCluster.GetCircuitBreakers().GetThresholds()[0].GetMaxRequests().GetValue()).
				To(Equal(uint32(wafMaxRequests)))
			Expect(wafCluster.GetTypedExtensionProtocolOptions()).To(HaveKey("envoy.extensions.upstreams.http.v3.HttpProtocolOptions"))

			connectionsMonitor := bootstrap.GetOverloadManager().GetResourceMonitors()[1]
			Expect(connectionsMonitor.GetName()).To(Equal("envoy.resource_monitors.global_downstream_max_connections"))
			connectionsConf := unmarshalAny(connectionsMonitor.GetTypedConfig(), &downstream_connectionsv3.DownstreamConnectionsConfig{})
			Expect(connectionsConf.GetMaxActiveDownstreamConnections()).To(Equal(int64(70000)))
		})

		It("should write config as json file", func() {
			configFile, err := runner.makeConfigFile(tmpDir)
			Expect(err).ToNot(HaveOccurred())
			Expect(filepath.Dir(configFile)).To(Equal(tmpDir))

			b, err := os.ReadFile(configFile)
			Expect(err).ToNot(HaveOccurred())
			bootstrap := &bootstrapv3.Bootstrap{}
			Expect(protojson.Unmarshal(b, bootstrap)).To(Succeed())
			Expect(bootstrap.GetNode().GetId()).To(Equal("starling-agent"))
		})
	})

	Context("memoryMonitor", func() {
		heapLimit := func(conf *anypb.Any) uint64 {
			GinkgoHelper()
			return unmarshalAny(conf, &fixed_heapv3.FixedHeapConfig{}).GetMaxHeapSizeBytes()
		}

		It("should watch the heap of a child envoy sharing the agent's cgroup", func() {
			name, conf, err := runner.memoryMonitor()
			Expect(err).ToNot(HaveOccurred())
			Expect(name).To(Equal("envoy.resource_monitors.fixed_heap"))
			Expect(heapLimit(conf)).To(Equal(runner.memory.Envoy))
		})

		It("should let envoy read its own cgroup under a service on linux", func() {
			runner.config = &config.EnvoyConfig{EnvoyService: &config.EnvoyService{}}
			name, conf, err := runner.memoryMonitor()
			Expect(err).ToNot(HaveOccurred())
			if runtime.GOOS != "linux" {
				Expect(name).To(Equal("envoy.resource_monitors.fixed_heap"))
				return
			}
			Expect(name).To(Equal("envoy.resource_monitors.cgroup_memory"))
			hostMemory := memory.TotalMemory()
			cgroupConf := unmarshalAny(conf, &cgroup_memoryv3.CgroupMemoryConfig{})
			Expect(cgroupConf.GetMaxMemoryBytes()).To(Equal(hostMemory * 60 / 100))
		})
	})

	Context("getLogLevel", func() {
		BeforeEach(func() {
			DeferCleanup(zerolog.SetGlobalLevel, zerolog.GlobalLevel())
		})

		It("should use configured envoy log level first", func() {
			runner.config.EnvoyExec.LogLevel = "trace"
			Expect(runner.getLogLevel()).To(Equal("trace"))
		})

		DescribeTable("should follow agent log level",
			func(level zerolog.Level, expected string) {
				zerolog.SetGlobalLevel(level)
				Expect(runner.getLogLevel()).To(Equal(expected))
			},
			Entry("trace", zerolog.TraceLevel, "trace"),
			Entry("debug", zerolog.DebugLevel, "debug"),
			Entry("info", zerolog.InfoLevel, "info"),
			Entry("warn", zerolog.WarnLevel, "warn"),
			Entry("error", zerolog.ErrorLevel, "error"),
			Entry("fatal", zerolog.FatalLevel, "critical"),
			Entry("panic", zerolog.PanicLevel, "critical"),
		)
	})

	Context("running as exec", func() {
		var argsFile string

		BeforeEach(func() {
			argsFile = filepath.Join(tmpDir, "args")
		})

		It("should start envoy with generated config and stop it gracefully on context cancel", func() {
			runner.config.EnvoyExec.BinPath = writeFakeEnvoy(tmpDir, argsFile, "trap 'exit 0' TERM\nwhile true; do sleep 0.1; done")
			runner.config.EnvoyExec.LogLevel = "debug"
			ctx, cancel := context.WithCancel(context.Background())
			errChan := make(chan error, 1)
			go func() {
				errChan <- runner.Run(ctx)
			}()

			Eventually(argsFile, 5*time.Second).Should(BeAnExistingFile())
			Eventually(func() string {
				b, _ := os.ReadFile(argsFile)
				return string(b)
			}).Should(And(ContainSubstring("-c "), ContainSubstring("-l debug"), ContainSubstring("--log-format")))

			Expect(runner.IsEnvoyAlive()).To(Succeed())
			cancel()
			Eventually(errChan, 5*time.Second).Should(Receive(BeNil()))
		})

		It("should return when envoy exits by itself", func() {
			runner.config.EnvoyExec.BinPath = writeFakeEnvoy(tmpDir, argsFile, "exit 3")
			errChan := make(chan error, 1)
			go func() {
				errChan <- runner.Run(context.Background())
			}()
			var err error
			Eventually(errChan, 5*time.Second).Should(Receive(&err))
			Expect(err).To(MatchError(ContainSubstring("envoy exited with error")))
		})

		It("should kill envoy when it does not stop in time", func() {
			// SIGTERM must arrive once ignored, the args file is written before the trap
			trappedFile := filepath.Join(tmpDir, "trapped")
			runner.config.EnvoyExec.BinPath = writeFakeEnvoy(tmpDir, argsFile, "trap '' TERM\ntouch "+trappedFile+"\nwhile true; do sleep 0.1; done")
			runner.stopTimeout = 200 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			errChan := make(chan error, 1)
			go func() {
				errChan <- runner.Run(ctx)
			}()
			Eventually(trappedFile, 5*time.Second).Should(BeAnExistingFile())
			cancel()
			Eventually(errChan, 5*time.Second).Should(Receive(BeNil()))
		})

		It("should fail when envoy is killed from outside", func() {
			runner.config.EnvoyExec.BinPath = writeFakeEnvoy(tmpDir, argsFile, "kill -9 $$")
			Expect(runner.Run(context.Background())).To(MatchError(ContainSubstring("envoy exited with error")))
		})

		It("should fail when envoy binary does not exist", func() {
			runner.config.EnvoyExec.BinPath = filepath.Join(tmpDir, "does-not-exist")
			Expect(runner.Run(context.Background())).To(MatchError(ContainSubstring("failed to start envoy")))
		})
	})

	Context("running as service", func() {
		BeforeEach(func() {
			runner.config.EnvoyExec = nil
			runner.config.EnvoyService = &config.EnvoyService{
				ConfigFilePath: filepath.Join(tmpDir, "envoy.json"),
				GetPidCommand:  []string{"sh", "-c", fmt.Sprintf("echo %d", os.Getpid())},
				StartCommand:   []string{"true"},
			}
		})

		It("should get envoy pid from command", func() {
			pid, err := runner.getPid(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(pid).To(Equal(os.Getpid()))
			Expect(runner.IsEnvoyAlive()).To(Succeed())
		})

		DescribeTable("should fail on invalid pid",
			func(command []string, errSubstring string) {
				runner.config.EnvoyService.GetPidCommand = command
				_, err := runner.getPid(context.Background())
				Expect(err).To(MatchError(ContainSubstring(errSubstring)))
				Expect(runner.IsEnvoyAlive()).ToNot(Succeed())
			},
			Entry("command fails", []string{"false"}, "failed to get pid"),
			Entry("not a number", []string{"echo", "not-a-pid"}, "invalid syntax"),
			Entry("zero pid", []string{"echo", "0"}, "pid is 0"),
		)

		It("should give up on a get-pid command that hangs", func() {
			runner.config.EnvoyService.GetPidCommand = []string{"sleep", "60"}
			done := make(chan error, 1)
			go func() { done <- runner.IsEnvoyAlive() }()
			Eventually(done, 6*time.Second).Should(Receive(HaveOccurred()))
		})

		It("should run start command", func() {
			Expect(runner.startService(context.Background())).To(Succeed())
			runner.config.EnvoyService.StartCommand = []string{"false"}
			Expect(runner.startService(context.Background())).To(MatchError(ContainSubstring("failed to start service")))
		})

		It("should compute md5 of a file", func() {
			file := filepath.Join(tmpDir, "file")
			Expect(os.WriteFile(file, []byte("content"), 0644)).To(Succeed())
			sum, err := runner.md5File(file)
			Expect(err).ToNot(HaveOccurred())
			Expect(sum).To(Equal("9a0364b9e99bb480dd25e1f0284c8555"))

			_, err = runner.md5File(filepath.Join(tmpDir, "not-exists"))
			Expect(err).To(HaveOccurred())
		})
	})
})

var _ = Describe("Access log", func() {
	AfterEach(func() {
		SetAccessLogStdoutInJson(false)
	})

	It("should switch access log format to json", func() {
		SetAccessLogStdoutInJson(true)
		expectValidEnvoyConfig(accessLogStdout)
		jsonFormat := accessLogStdout.GetLogFormat().GetJsonFormat().AsMap()
		Expect(jsonFormat).To(HaveKeyWithValue("response_code", "%RESPONSE_CODE%"))
		Expect(jsonFormat).To(HaveKeyWithValue("upstream_host", "%UPSTREAM_HOST%"))

		SetAccessLogStdoutInJson(false)
		Expect(accessLogStdout.GetAccessLogFormat()).To(BeNil())
	})
})
