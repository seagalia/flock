//go:build linux

package app

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("sysctlSet", func() {
	var (
		setValues map[string]string
		failOn    map[string]bool
	)

	BeforeEach(func() {
		setValues = map[string]string{}
		failOn = map[string]bool{}
		prevSetter := sysctlSetter
		DeferCleanup(func() {
			sysctlSetter = prevSetter
		})
		sysctlSetter = func(name, value string) error {
			if failOn[name] {
				return fmt.Errorf("permission denied")
			}
			setValues[name] = value
			return nil
		}
	})

	It("sets every value", func() {
		Expect(sysctlSet(false, map[string]string{"net.ipv4.ip_forward": "1", "fs.file-max": "100"})).To(Succeed())
		Expect(setValues).To(Equal(map[string]string{"net.ipv4.ip_forward": "1", "fs.file-max": "100"}))
	})

	// these keys are host global: missing or refused in a non initial network namespace (container)
	DescribeTable("tolerates failures of host global sysctls in permissive mode",
		func(key string) {
			failOn[key] = true
			Expect(sysctlSet(true, map[string]string{key: "100", "net.ipv4.ip_forward": "1"})).To(Succeed())
			Expect(setValues).To(HaveKeyWithValue("net.ipv4.ip_forward", "1"))
		},
		Entry(nil, "fs.file-max"),
		Entry(nil, "net.netfilter.nf_conntrack_buckets"),
		Entry(nil, "net.netfilter.nf_conntrack_max"),
		Entry(nil, "net.core.rmem_max"),
		Entry(nil, "net.core.wmem_max"),
		Entry(nil, "net.core.netdev_max_backlog"),
	)

	It("fails on any other sysctl failure, even in permissive mode", func() {
		failOn["net.ipv4.ip_forward"] = true
		err := sysctlSet(true, map[string]string{"net.ipv4.ip_forward": "1", "fs.file-max": "100"})
		Expect(err).To(MatchError(ContainSubstring("failed to set sysctl net.ipv4.ip_forward")))
	})

	It("fails on host global sysctls failures when not in permissive mode", func() {
		failOn["net.netfilter.nf_conntrack_max"] = true
		err := sysctlSet(false, map[string]string{"net.netfilter.nf_conntrack_max": "262144"})
		Expect(err).To(MatchError(ContainSubstring("failed to set sysctl net.netfilter.nf_conntrack_max")))
	})
})
