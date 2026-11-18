package ipvsctl_test

import (
	"net"
	"testing"

	"github.com/cloudflare/ipvs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/agent/starling/ipvsctl"
	"github.com/seagalia/flock/sdk/logging"
)

func TestIpvsctl(t *testing.T) {
	RegisterFailHandler(Fail)
	if err := logging.Setup(logging.Options{Console: GinkgoWriter}); err != nil {
		t.Fatal(err)
	}
	RunSpecs(t, "Ipvsctl Suite")
}

func makeService(cidr string, port uint16, protocol ipvs.Protocol) ipvs.Service {
	_, ipNet, err := net.ParseCIDR(cidr)
	Expect(err).ToNot(HaveOccurred())
	family := ipvs.INET
	if ipNet.IP.To4() == nil {
		family = ipvs.INET6
	}
	return ipvs.Service{
		Address:   ipvsctl.AddrFromIP(ipNet.IP),
		Netmask:   ipvsctl.MaskFromIPMask(ipNet.Mask),
		Scheduler: "rr",
		Port:      port,
		Family:    family,
		Protocol:  protocol,
	}
}

func makeDestination(ip string, port uint16) ipvs.Destination { //nolint:unparam // the call sites show the value
	parsedIp := net.ParseIP(ip)
	family := ipvs.INET
	if parsedIp.To4() == nil {
		family = ipvs.INET6
	}
	return ipvs.Destination{
		Address:   ipvsctl.AddrFromIP(parsedIp),
		FwdMethod: ipvs.Masquerade,
		Weight:    1,
		Port:      port,
		Family:    family,
	}
}
