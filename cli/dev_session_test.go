package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Dev session", func() {
	var pidPath string

	BeforeEach(func() {
		home := GinkgoT().TempDir()
		GinkgoT().Setenv("HOME", home)
		pidPath = filepath.Join(home, ".flock", "dev", "dev.pid")
		Expect(os.MkdirAll(filepath.Dir(pidPath), 0755)).To(Succeed())
		previousConfigPath := configPath
		DeferCleanup(func() { configPath = previousConfigPath })
	})

	writePid := func(pid int) {
		Expect(os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0644)).To(Succeed())
	}

	prepare := func(cliConfig string) {
		commands := &Commands{CliConfig: cliConfig}
		Expect(prepareCommand(context.Background(), commands, &Hook{}, nil)).To(Succeed())
	}

	It("uses the dev cli config while flock dev runs", func() {
		writePid(os.Getpid())

		prepare(defaultConfigPath)

		Expect(configPath).To(Equal(DevCliConfigPath))
	})

	It("keeps an explicit cli config while flock dev runs", func() {
		writePid(os.Getpid())

		prepare("/tmp/other-config.json")

		Expect(configPath).To(Equal("/tmp/other-config.json"))
	})

	It("ignores a pid file of a dead process", func() {
		// pids are far below this value on linux and darwin
		writePid(99999999)

		prepare(defaultConfigPath)

		Expect(configPath).To(Equal(defaultConfigPath))
	})

	It("uses the default cli config without dev session", func() {
		prepare(defaultConfigPath)

		Expect(configPath).To(Equal(defaultConfigPath))
	})
})
