package ctl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"

	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/agent/starling/memsplit"
	wafapp "github.com/seagalia/flock/agent/starling/waf/app"
	"github.com/seagalia/flock/agent/starling/waf/client"
	svcregister "github.com/seagalia/flock/sdk/starling_waf/services/register/v1"
)

// writeFakeWaf records its args in argsFile then runs body
func writeFakeWaf(dir, argsFile, body string) string {
	GinkgoHelper()
	script := filepath.Join(dir, "fake-starling-waf")
	content := fmt.Sprintf("#!/bin/sh\necho \"$@\" > %s\n%s\n", argsFile, body)
	Expect(os.WriteFile(script, []byte(content), 0o755)).To(Succeed())
	return script
}

var _ = Describe("Runner", func() {
	var (
		tmpDir string
		runner *Runner
	)

	BeforeEach(func() {
		tmpDir = GinkgoT().TempDir()
		wafConfig := &config.WafConfig{WafExec: &config.WafExec{BinPath: "starling-waf"}}
		runner = NewRunner(wafConfig, memsplit.New(wafConfig, true))
	})

	Context("getLogLevel", func() {
		BeforeEach(func() {
			DeferCleanup(zerolog.SetGlobalLevel, zerolog.GlobalLevel())
		})

		It("uses the configured starling-waf log level first", func() {
			runner.config.WafExec.LogLevel = "trace"
			Expect(runner.getLogLevel()).To(Equal("trace"))
		})

		DescribeTable("follows the agent log level",
			func(level zerolog.Level, expected string) {
				zerolog.SetGlobalLevel(level)
				Expect(runner.getLogLevel()).To(Equal(expected))
			},
			Entry("trace", zerolog.TraceLevel, "trace"),
			Entry("debug", zerolog.DebugLevel, "debug"),
			Entry("info", zerolog.InfoLevel, "info"),
			Entry("warn", zerolog.WarnLevel, "warn"),
			Entry("error", zerolog.ErrorLevel, "error"),
		)
	})

	Context("running as exec", func() {
		var argsFile string

		BeforeEach(func() {
			argsFile = filepath.Join(tmpDir, "args")
		})

		It("starts starling-waf and stops it gracefully on context cancel", func() {
			runner.config.WafExec.BinPath = writeFakeWaf(tmpDir, argsFile, "trap 'exit 0' TERM\nwhile true; do sleep 0.1; done")
			runner.config.WafExec.LogLevel = "debug"
			ctx, cancel := context.WithCancel(context.Background())
			errChan := make(chan error, 1)
			go func() {
				errChan <- runner.Run(ctx)
			}()

			Eventually(func() string {
				b, _ := os.ReadFile(argsFile)
				return string(b)
			}, 5*time.Second).Should(And(ContainSubstring("-l debug"), ContainSubstring("-s "+config.DefaultSocketPath), ContainSubstring("-m "+config.DefaultMetricsSocketPath)))

			Expect(runner.IsAlive()).To(Succeed())
			cancel()
			Eventually(errChan, 5*time.Second).Should(Receive(BeNil()))
		})

		It("returns when starling-waf exits by itself", func() {
			runner.config.WafExec.BinPath = writeFakeWaf(tmpDir, argsFile, "exit 3")
			errChan := make(chan error, 1)
			go func() {
				errChan <- runner.Run(context.Background())
			}()
			var err error
			Eventually(errChan, 5*time.Second).Should(Receive(&err))
			Expect(err).To(MatchError(ContainSubstring("starling-waf exited with error")))
		})

		It("kills starling-waf when it does not stop in time", func() {
			// SIGTERM must arrive once ignored, the args file is written before the trap
			trappedFile := filepath.Join(tmpDir, "trapped")
			runner.config.WafExec.BinPath = writeFakeWaf(tmpDir, argsFile, "trap '' TERM\ntouch "+trappedFile+"\nwhile true; do sleep 0.1; done")
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

		It("fails when starling-waf is killed from outside", func() {
			runner.config.WafExec.BinPath = writeFakeWaf(tmpDir, argsFile, "kill -9 $$")
			Expect(runner.Run(context.Background())).To(MatchError(ContainSubstring("starling-waf exited with error")))
		})

		It("fails when the starling-waf binary does not exist", func() {
			runner.config.WafExec.BinPath = filepath.Join(tmpDir, "does-not-exist")
			Expect(runner.Run(context.Background())).To(MatchError(ContainSubstring("failed to start starling-waf")))
		})
	})

	Context("running as service", func() {
		BeforeEach(func() {
			runner.config.WafExec = nil
			runner.config.WafService = &config.WafService{
				SocketPath:        filepath.Join(tmpDir, "waf.sock"),
				MetricsSocketPath: filepath.Join(tmpDir, "metrics.sock"),
				GetPidCommand:     []string{"sh", "-c", fmt.Sprintf("echo %d", os.Getpid())},
				StartCommand:      []string{"true"},
			}
		})

		It("gets the starling-waf pid from the command", func() {
			pid, err := runner.getPid(context.Background())
			Expect(err).ToNot(HaveOccurred())
			Expect(pid).To(Equal(os.Getpid()))
			Expect(runner.IsAlive()).To(Succeed())
		})

		DescribeTable("fails on an invalid pid",
			func(command []string, errSubstring string) {
				runner.config.WafService.GetPidCommand = command
				_, err := runner.getPid(context.Background())
				Expect(err).To(MatchError(ContainSubstring(errSubstring)))
				Expect(runner.IsAlive()).ToNot(Succeed())
			},
			Entry("command fails", []string{"false"}, "failed to get pid"),
			Entry("not a number", []string{"echo", "not-a-pid"}, "invalid syntax"),
			Entry("zero pid", []string{"echo", "0"}, "pid is 0"),
		)

		It("gives up on a get-pid command that hangs", func() {
			runner.config.WafService.GetPidCommand = []string{"sleep", "60"}
			done := make(chan error, 1)
			go func() { done <- runner.IsAlive() }()
			Eventually(done, 6*time.Second).Should(Receive(HaveOccurred()))
		})

		It("runs the start command", func() {
			Expect(runner.startService(context.Background())).To(Succeed())
			runner.config.WafService.StartCommand = []string{"false"}
			Expect(runner.startService(context.Background())).To(MatchError(ContainSubstring("failed to start service")))
		})

		It("follows the log files of the service until its context ends", func() {
			runner.config.WafService.StdoutFilePath = filepath.Join(tmpDir, "stdout.log")
			runner.config.WafService.StderrFilePath = filepath.Join(tmpDir, "stderr.log")
			Expect(os.WriteFile(runner.config.WafService.StdoutFilePath, nil, 0o644)).To(Succeed())
			Expect(os.WriteFile(runner.config.WafService.StderrFilePath, nil, 0o644)).To(Succeed())
			ctx, cancel := context.WithCancel(context.Background())
			errChan := make(chan error, 1)
			go func() {
				errChan <- runner.Run(ctx)
			}()

			Consistently(errChan, 300*time.Millisecond).ShouldNot(Receive())
			cancel()
			Eventually(errChan, 5*time.Second).Should(Receive(BeNil()))
		})

		It("fails when the start command fails", func() {
			runner.config.WafService.StartCommand = []string{"false"}
			Expect(runner.Run(context.Background())).To(MatchError(ContainSubstring("failed to start service")))
		})
	})

	Context("running as embedded", func() {
		BeforeEach(func() {
			runner.config = &config.WafConfig{WafEmbedded: true}
		})

		It("serves the WAF in the agent process until its context ends", func() {
			ctx, cancel := context.WithCancel(context.Background())
			errChan := make(chan error, 1)
			go func() {
				errChan <- runner.Run(ctx)
			}()
			Eventually(func() os.FileMode {
				info, _ := os.Stat(config.DefaultSocketPath)
				if info == nil {
					return 0
				}
				return info.Mode().Type()
			}, 10*time.Second).Should(Equal(os.ModeSocket))

			wafClient, err := client.NewClient(config.DefaultSocketPath)
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(wafClient.Close)
			_, err = wafClient.RegisterWafConfig(context.Background(), &svcregister.RegisterWafConfigRequest{
				Identifier: "vs-1",
				Config:     &svcregister.RegisterWafConfigRequest_DetectOnly{DetectOnly: true},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(runner.IsAlive()).To(Succeed())

			cancel()
			Eventually(errChan, 10*time.Second).Should(Receive(BeNil()))
			Expect(config.DefaultSocketPath).ToNot(BeAnExistingFile())
		})
	})

	It("is alive when it runs starling-waf itself", func() {
		Expect(runner.IsAlive()).To(Succeed())
		runner.config = &config.WafConfig{WafEmbedded: true}
		Expect(runner.IsAlive()).To(Succeed())
	})

	It("checks an external starling-waf through its health service", func() {
		// macOS refuses unix socket paths over 104 bytes, and the one of GinkgoT().TempDir() is longer
		dir, err := os.MkdirTemp("", "waf")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		socketPath := filepath.Join(dir, "waf.sock")
		runner.config = &config.WafConfig{WafExternal: &config.WafExternal{
			SocketPath:        socketPath,
			MetricsSocketPath: filepath.Join(dir, "waf-metrics.sock"),
		}}
		Expect(runner.IsAlive()).ToNot(Succeed())

		app, err := wafapp.NewApp(socketPath, "", 1<<30)
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		errChan := make(chan error, 1)
		go func() { errChan <- app.RunWithContext(ctx) }()
		DeferCleanup(func() {
			cancel()
			Eventually(errChan, 10*time.Second).Should(Receive())
		})

		Eventually(runner.IsAlive, 10*time.Second).Should(Succeed())
	})
})
