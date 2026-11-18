package ipvsctl

import (
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ensureIpvsLoaded", func() {
	It("does not load a module already loaded", func() {
		sysModule := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(sysModule, ipvsModule), 0o755)).To(Succeed())
		called := false
		Expect(ensureIpvsLoaded(sysModule, func() error {
			called = true
			return fmt.Errorf("must not be called")
		})).To(Succeed())
		Expect(called).To(BeFalse())
	})

	It("loads a module not loaded yet", func() {
		called := false
		Expect(ensureIpvsLoaded(GinkgoT().TempDir(), func() error {
			called = true
			return nil
		})).To(Succeed())
		Expect(called).To(BeTrue())
	})

	It("returns the load error", func() {
		err := ensureIpvsLoaded(GinkgoT().TempDir(), func() error { return fmt.Errorf("exec format error") })
		Expect(err).To(MatchError(ContainSubstring("failed to load ip_vs kernel module: exec format error")))
	})
})
