//go:build linux

package nm

import (
	"net"

	"github.com/jsimonetti/rtnetlink/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"golang.org/x/sys/unix"
)

func addrOn(index uint32, family int, ip string) rtnetlink.AddressMessage {
	return rtnetlink.AddressMessage{
		Family:     uint8(family),
		Index:      index,
		Attributes: &rtnetlink.AddressAttributes{Address: net.ParseIP(ip), Local: net.ParseIP(ip)},
	}
}

// only pure helpers of the linux network manager are covered here,
// real netlink and nftables calls need privileges and are left to e2e tests
var _ = Describe("Linux network manager helpers", func() {
	DescribeTable("addrFamily",
		func(ip net.IP, expected int, expectErr bool) {
			family, err := addrFamily(ip)
			if expectErr {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).ToNot(HaveOccurred())
			Expect(family).To(Equal(expected))
		},
		Entry("ipv4", net.ParseIP("10.0.0.1"), unix.AF_INET, false),
		Entry("ipv6", net.ParseIP("2001:db8::1"), unix.AF_INET6, false),
		Entry("invalid", net.IP{1, 2}, 0, true),
	)

	DescribeTable("leaves a wildcard vip off the dummy interface",
		func(cidr string) {
			_, ipNet, err := net.ParseCIDR(cidr)
			Expect(err).ToNot(HaveOccurred())
			// no netlink connection: a call reaching the kernel would panic
			n := &nmLinux{}
			Expect(n.AddVip(ipNet)).To(Succeed())
			Expect(n.DelVip(ipNet)).To(Succeed())
		},
		Entry("ipv4", "0.0.0.0/32"),
		Entry("ipv6", "::/128"),
	)

	DescribeTable("findHolder",
		func(addrs []rtnetlink.AddressMessage, ip string, expectedHolder uint32, expectedHeld bool) {
			parsed := net.ParseIP(ip)
			af, err := addrFamily(parsed)
			Expect(err).ToNot(HaveOccurred())
			holder, held := findHolder(addrs, parsed, af, 5)
			Expect(held).To(Equal(expectedHeld))
			Expect(holder).To(Equal(expectedHolder))
		},
		Entry("ip on another interface",
			[]rtnetlink.AddressMessage{addrOn(2, unix.AF_INET, "10.0.0.1")}, "10.0.0.1", uint32(2), true),
		Entry("ip only on the dummy interface",
			[]rtnetlink.AddressMessage{addrOn(5, unix.AF_INET, "10.0.0.1")}, "10.0.0.1", uint32(0), false),
		Entry("another ip on another interface",
			[]rtnetlink.AddressMessage{addrOn(2, unix.AF_INET, "10.0.0.2")}, "10.0.0.1", uint32(0), false),
		Entry("ipv6 ip on another interface",
			[]rtnetlink.AddressMessage{addrOn(2, unix.AF_INET6, "2001:db8::1")}, "2001:db8::1", uint32(2), true),
		Entry("ipv4-mapped ipv6 address of an ipv4 vip, net.IP.Equal alone would match it",
			[]rtnetlink.AddressMessage{addrOn(2, unix.AF_INET6, "::ffff:10.0.0.1")}, "10.0.0.1", uint32(0), false),
	)
})
