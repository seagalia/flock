//go:build !windows

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// probeCmd is a command only the specs know. The specs run it in a child process (see init) because sending a signal to
// the ginkgo process would interrupt the whole suite.
type probeCmd struct {
	mode string
}

func (p *probeCmd) NoCheckUpdate() bool { return true }

func (p *probeCmd) Run(ctx context.Context) error {
	fmt.Println("ready")
	switch p.mode {
	case "cancel", "fail":
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		select {
		case <-ctx.Done():
			fmt.Println("ctx error:", ctx.Err())
			fmt.Println("is canceled:", errors.Is(ctx.Err(), context.Canceled))
		case <-time.After(5 * time.Second):
			fmt.Println("ctx never cancelled")
		}
		if p.mode == "fail" {
			return ctx.Err()
		}
	case "ignore":
		time.Sleep(30 * time.Second)
	}
	return nil
}

// init turns the test binary into the probe command when a spec re-executes it as a child.
func init() {
	mode := os.Getenv("FLOCK_TEST_PROBE_MODE")
	if mode == "" {
		return
	}
	c := &CLI{}
	parser, err := newParser(c, kong.DynamicCommand("probe", "test probe", "", &probeCmd{mode: mode}))
	if err != nil {
		panic(err)
	}
	execute(parser, c, []string{"probe"})
	os.Exit(0)
}

var _ = Describe("root context", func() {
	startProbe := func(mode string) (*exec.Cmd, *bufio.Reader) {
		GinkgoHelper()
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "FLOCK_TEST_PROBE_MODE="+mode)
		stdout, err := cmd.StdoutPipe()
		Expect(err).ToNot(HaveOccurred())
		Expect(cmd.Start()).To(Succeed())
		DeferCleanup(func() { _ = cmd.Process.Kill() })
		reader := bufio.NewReader(stdout)
		line, err := reader.ReadString('\n')
		Expect(err).ToNot(HaveOccurred())
		Expect(line).To(Equal("ready\n"))
		return cmd, reader
	}

	It("binds a context into Run that a SIGTERM cancels", func() {
		cmd, reader := startProbe("cancel")

		output, err := io.ReadAll(reader)
		Expect(err).ToNot(HaveOccurred())
		Expect(cmd.Wait()).To(Succeed())
		Expect(string(output)).To(ContainSubstring("ctx error: context canceled"))
		Expect(string(output)).To(ContainSubstring("is canceled: true"))
	})

	It("reports a command cancelled by the signal as interrupted", func() {
		cmd, reader := startProbe("fail")

		output, err := io.ReadAll(reader)
		Expect(err).ToNot(HaveOccurred())
		Expect(cmd.Wait()).To(HaveOccurred())
		Expect(string(output)).To(ContainSubstring("Error interrupted"))
	})

	It("kills on a second signal", func() {
		cmd, _ := startProbe("ignore")
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		Expect(cmd.Process.Signal(syscall.SIGTERM)).To(Succeed())
		time.Sleep(200 * time.Millisecond)
		Expect(cmd.Process.Signal(syscall.SIGTERM)).To(Succeed())

		Eventually(done, 5*time.Second).Should(Receive())
		status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
		Expect(ok).To(BeTrue())
		Expect(status.Signaled()).To(BeTrue())
		Expect(status.Signal()).To(Equal(syscall.SIGTERM))
	})
})
