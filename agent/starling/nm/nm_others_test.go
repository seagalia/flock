//go:build !linux

package nm_test

import (
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/agent/starling/config"
	oerrors "github.com/seagalia/flock/sdk/errors"

	. "github.com/seagalia/flock/agent/starling/nm"
)

var _ = Describe("NetworkManager on non linux platform", func() {
	It("should not be implemented", func() {
		netMan, err := NewNetworkManager(config.NetworkManagerConfig{}, nil)
		Expect(err).ToNot(HaveOccurred())
		_, ipNet, err := net.ParseCIDR("10.0.0.1/32")
		Expect(err).ToNot(HaveOccurred())

		_, err = netMan.RouteGetAll(net.ParseIP("10.0.0.1"))
		Expect(oerrors.IsErrNotImplemented(err)).To(BeTrue())
		for _, err := range []error{
			netMan.AddVip(ipNet),
			netMan.DelVip(ipNet),
			netMan.AddSnat(ipNet, 80, false, false),
			netMan.DelSnat(ipNet, 80, false, false),
			netMan.AddConnLimit(ipNet, 80, false, 10, true, true),
			netMan.DelConnLimit(ipNet, 80, false, 10, true, true),
			netMan.AddConnRateLimit(ipNet, 80, false, "10/second", 5, true, true),
			netMan.DelConnRateLimit(ipNet, 80, false, "10/second", 5, true, true),
			netMan.AddMarkLog(ipNet, 80, false),
			netMan.DelMarkLog(ipNet, 80, false),
			netMan.AddNfLog(ipNet, 80, "prefix", false),
			netMan.DelNfLog(ipNet, 80, "prefix", false),
		} {
			Expect(oerrors.IsErrNotImplemented(err)).To(BeTrue())
		}
	})

	It("should have no-op snat and cleanup", func() {
		Expect(SnatInterfaces(config.NetworkManagerConfig{}, []string{"eth0"})).To(Succeed())
		Expect(Cleanup(config.NetworkManagerConfig{})).To(Succeed())
	})
})
