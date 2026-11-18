package ipvsctl_test

import (
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/agent/starling/ipvsctl"
)

var _ = Describe("ipvs conversions", func() {
	It("converts an ipv4 net.IP to an unmapped netip.Addr", func() {
		addr := AddrFromIP(net.ParseIP("10.0.0.1"))
		Expect(addr.Is4()).To(BeTrue())
		Expect(addr.String()).To(Equal("10.0.0.1"))
	})

	It("converts an ipv6 net.IP", func() {
		Expect(AddrFromIP(net.ParseIP("2001:db8::1")).String()).To(Equal("2001:db8::1"))
	})

	It("converts net.IPMask for both families", func() {
		_, v4, _ := net.ParseCIDR("10.0.0.0/24")
		_, v6, _ := net.ParseCIDR("2001:db8::/64")
		Expect(MaskFromIPMask(v4.Mask).AsSlice()).To(Equal([]byte(v4.Mask)))
		Expect(MaskFromIPMask(v6.Mask).AsSlice()).To(Equal([]byte(v6.Mask)))
	})
})
