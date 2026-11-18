package nm_test

import (
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/agent/starling/config"

	. "github.com/seagalia/flock/agent/starling/nm"
)

var _ = Describe("Vips", func() {
	It("should list the addresses of the dummy interface", func() {
		var loopback string
		ifaces, err := net.Interfaces()
		Expect(err).ToNot(HaveOccurred())
		for _, iface := range ifaces {
			if iface.Flags&net.FlagLoopback != 0 {
				loopback = iface.Name
			}
		}
		Expect(loopback).ToNot(BeEmpty())

		vips, err := Vips(config.NetworkManagerConfig{DummyInterfaceName: loopback})
		Expect(err).ToNot(HaveOccurred())
		Expect(vips).To(ContainElement(WithTransform(net.IP.String, Equal("127.0.0.1"))))
	})
	It("should give no address when the interface doesn't exist", func() {
		vips, err := Vips(config.NetworkManagerConfig{DummyInterfaceName: "flock-missing0"})
		Expect(err).ToNot(HaveOccurred())
		Expect(vips).To(BeEmpty())
	})
})
