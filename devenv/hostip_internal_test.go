package devenv

import (
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("isSnatIface", func() {
	ipNet := func(cidr string) net.Addr {
		GinkgoHelper()
		ip, network, err := net.ParseCIDR(cidr)
		Expect(err).ToNot(HaveOccurred())
		network.IP = ip
		return network
	}

	DescribeTable("keeps the interfaces a connection to a backend can leave by",
		func(name string, flags net.Flags, cidrs []string, expected bool) {
			addrs := make([]net.Addr, 0, len(cidrs))
			for _, cidr := range cidrs {
				addrs = append(addrs, ipNet(cidr))
			}
			Expect(isSnatIface(net.Interface{Name: name, Flags: flags}, addrs)).To(Equal(expected))
		},
		Entry("a network of the container", "eth1", net.FlagUp, []string{"172.21.0.3/16"}, true),
		Entry("a global v6", "eth2", net.FlagUp, []string{"fe80::1/64", "fd00::3/64"}, true),
		Entry("down", "eth3", net.Flags(0), []string{"172.22.0.3/16"}, false),
		Entry("the loopback", "lo", net.FlagUp|net.FlagLoopback, []string{"127.0.0.1/8"}, false),
		Entry("the interface of the vips", "flock0", net.FlagUp, []string{"10.0.0.5/32"}, false),
		Entry("link-local only", "veth0", net.FlagUp, []string{"fe80::1/64"}, false),
		Entry("no address", "tun0", net.FlagUp, nil, false),
	)
})

var _ = Describe("keepSanIP", func() {
	DescribeTable("keeps the addresses a client can dial",
		func(ip string, expected bool) {
			Expect(keepSanIP(net.ParseIP(ip))).To(Equal(expected))
		},
		Entry("loopback", "127.0.0.1", false),
		Entry("link-local v6", "fe80::1", false),
		Entry("link-local v4", "169.254.1.1", false),
		Entry("v4", "10.0.0.5", true),
		Entry("ula v6", "fd00::5", true),
	)
})
