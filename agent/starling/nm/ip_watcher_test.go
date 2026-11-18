package nm

import (
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/sys/unix"
)

func ips(values ...string) []net.IP {
	out := make([]net.IP, 0, len(values))
	for _, v := range values {
		out = append(out, net.ParseIP(v))
	}
	return out
}

var _ = Describe("expandWildcard", func() {
	const dummy = uint32(9)
	addrs := []machineAddr{
		{ifaceIndex: 1, ip: net.ParseIP("127.0.0.1")},
		{ifaceIndex: 1, ip: net.ParseIP("::1")},
		{ifaceIndex: 2, ip: net.ParseIP("10.0.0.5")},
		{ifaceIndex: 2, ip: net.ParseIP("fe80::1")},
		{ifaceIndex: 2, ip: net.ParseIP("169.254.1.1")},
		{ifaceIndex: 2, ip: net.ParseIP("2001:db8::5")},
		{ifaceIndex: 3, ip: net.ParseIP("10.0.0.2")},
		{ifaceIndex: 3, ip: net.ParseIP("10.0.0.5")},
		{ifaceIndex: dummy, ip: net.ParseIP("192.0.2.1")},
		{ifaceIndex: dummy, ip: net.ParseIP("2001:db8::99")},
	}

	It("keeps the global IPv4 addresses sorted and without duplicates for 0.0.0.0", func() {
		got := expandWildcard(addrs, unix.AF_INET, dummy)
		Expect(got).To(HaveLen(2))
		Expect(got[0].Equal(net.ParseIP("10.0.0.2"))).To(BeTrue())
		Expect(got[1].Equal(net.ParseIP("10.0.0.5"))).To(BeTrue())
		Expect(got[0]).To(HaveLen(net.IPv4len))
	})

	It("keeps only the global IPv6 addresses for ::", func() {
		got := expandWildcard(addrs, unix.AF_INET6, dummy)
		Expect(got).To(HaveLen(1))
		Expect(got[0].Equal(net.ParseIP("2001:db8::5"))).To(BeTrue())
	})

	It("gives nothing when every address is excluded", func() {
		Expect(expandWildcard(addrs[:2], unix.AF_INET, dummy)).To(BeEmpty())
	})

	It("compares sets of addresses in order", func() {
		Expect(sameIPs(ips("10.0.0.1", "10.0.0.2"), ips("10.0.0.1", "10.0.0.2"))).To(BeTrue())
		Expect(sameIPs(ips("10.0.0.1"), ips("10.0.0.2"))).To(BeFalse())
		Expect(sameIPs(nil, ips("10.0.0.2"))).To(BeFalse())
	})
})

var _ = Describe("nextRetryWait", func() {
	It("doubles from the minimum and stops at the cap", func() {
		wait := retryMinWait
		seen := make([]time.Duration, 0, 8)
		seen = append(seen, wait)
		for range 7 {
			wait = nextRetryWait(wait)
			seen = append(seen, wait)
		}
		Expect(seen).To(Equal([]time.Duration{
			time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
			16 * time.Second, 30 * time.Second, 30 * time.Second, 30 * time.Second,
		}))
	})
})
