package app

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/starling/config"
)

var _ = Describe("decideRunEnvoy", func() {
	var envoyBin string

	BeforeEach(func() {
		envoyBin = filepath.Join(GinkgoT().TempDir(), "envoy")
		Expect(os.WriteFile(envoyBin, []byte("#!/bin/sh\n"), 0755)).To(Succeed())
	})

	appWith := func(devMode bool, envoyConf *config.EnvoyConfig) *App {
		return &App{cnf: &config.Config{DevMode: devMode, EnvoyConfig: envoyConf}}
	}

	It("should always run envoy outside dev mode", func() {
		missing := &config.EnvoyConfig{EnvoyExec: &config.EnvoyExec{BinPath: "/does/not/exist/envoy"}}
		Expect(appWith(false, missing).decideRunEnvoy()).To(BeTrue())
	})

	It("should run envoy in dev mode when the exec binary exists", func() {
		found := &config.EnvoyConfig{EnvoyExec: &config.EnvoyExec{BinPath: envoyBin}}
		Expect(appWith(true, found).decideRunEnvoy()).To(BeTrue())
	})

	It("should not run envoy in dev mode when the exec binary is missing", func() {
		missing := &config.EnvoyConfig{EnvoyExec: &config.EnvoyExec{BinPath: "/does/not/exist/envoy"}}
		Expect(appWith(true, missing).decideRunEnvoy()).To(BeFalse())
	})

	It("should not run envoy in dev mode with the service mode", func() {
		service := &config.EnvoyConfig{EnvoyService: &config.EnvoyService{ConfigFilePath: "/etc/envoy.json"}}
		Expect(appWith(true, service).decideRunEnvoy()).To(BeFalse())
	})
})
