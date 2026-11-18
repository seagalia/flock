package handlers_test

import (
	"errors"
	"github.com/cloudflare/ipvs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"os"

	. "github.com/seagalia/flock/agent/starling/handlers"
	"github.com/seagalia/flock/agent/starling/ipvsctl"
)

func ipvsService(cidr string, port uint16) *ipvs.Service {
	n := ipNet(cidr)
	return &ipvs.Service{
		Address:   ipvsctl.AddrFromIP(n.IP),
		Netmask:   ipvsctl.MaskFromIPMask(n.Mask),
		Scheduler: "rr",
		Port:      port,
		Family:    ipvs.INET,
		Protocol:  ipvs.TCP,
	}
}

type destination struct {
	Addr    string
	Weight  uint32
	FwdType ipvs.ForwardType
}

func listDestinations(service *ipvs.Service) []destination {
	GinkgoHelper()
	result := make([]destination, 0)
	dests, err := ipvsctl.Destinations(*service)
	if errors.Is(err, os.ErrNotExist) {
		return result
	}
	Expect(err).ToNot(HaveOccurred())
	for _, dest := range dests {
		result = append(result, destination{
			Addr:    dest.Address.Unmap().String() + ":" + itoa(int(dest.Port)),
			Weight:  dest.Weight,
			FwdType: dest.FwdMethod,
		})
	}
	return result
}

var _ = Describe("IpvsHandler", func() {
	var (
		service  *ipvs.Service
		handler  *IpvsHandler
		aBackend = makeBackend("backend-1")
	)

	BeforeEach(func() {
		service = ipvsService("10.0.0.1/32", 80)
		Expect(ipvsctl.CreateService(*service)).To(Succeed())
		handler = NewIpvsHandler(service, ipvs.Masquerade)
	})

	It("should create destinations on startup with weight shifted to avoid quiescent servers", func() {
		handler.OnStartup(aBackend, []*core.IPAddr{
			{Ip: "192.168.0.1", Port: 8080},
			{Ip: "192.168.0.2", Port: 8080, LoadBalancingWeight: 4},
		})
		Expect(listDestinations(service)).To(ConsistOf(
			destination{Addr: "192.168.0.1:8080", Weight: 1, FwdType: ipvs.Masquerade},
			destination{Addr: "192.168.0.2:8080", Weight: 5, FwdType: ipvs.Masquerade},
		))
	})

	It("should remove destinations not known anymore on startup", func() {
		handler.OnStartup(aBackend, []*core.IPAddr{{Ip: "192.168.0.1", Port: 8080}, {Ip: "192.168.0.2", Port: 8080}})
		handler.OnStartup(aBackend, []*core.IPAddr{{Ip: "192.168.0.2", Port: 8080, LoadBalancingWeight: 9}})
		Expect(listDestinations(service)).To(ConsistOf(
			destination{Addr: "192.168.0.2:8080", Weight: 10, FwdType: ipvs.Masquerade},
		))
	})

	It("should clean destinations when starting without endpoints", func() {
		handler.OnStartup(aBackend, []*core.IPAddr{{Ip: "192.168.0.1", Port: 8080}})
		handler.OnStartup(aBackend, []*core.IPAddr{})
		Expect(listDestinations(service)).To(BeEmpty())
	})

	It("should follow endpoints added and removed", func() {
		handler.OnEndpointAdded(aBackend, &core.IPAddr{Ip: "192.168.0.1", Port: 8080})
		handler.OnEndpointAdded(aBackend, &core.IPAddr{Ip: "192.168.0.2", Port: 8080})
		// adding again updates the destination
		handler.OnEndpointAdded(aBackend, &core.IPAddr{Ip: "192.168.0.2", Port: 8080, LoadBalancingWeight: 2})
		Expect(listDestinations(service)).To(ConsistOf(
			destination{Addr: "192.168.0.1:8080", Weight: 1, FwdType: ipvs.Masquerade},
			destination{Addr: "192.168.0.2:8080", Weight: 3, FwdType: ipvs.Masquerade},
		))

		handler.OnEndpointRemoved(aBackend, &core.IPAddr{Ip: "192.168.0.1", Port: 8080})
		Expect(listDestinations(service)).To(ConsistOf(
			destination{Addr: "192.168.0.2:8080", Weight: 3, FwdType: ipvs.Masquerade},
		))
	})

	It("should recreate destinations when forward type changes", func() {
		handler.OnStartup(aBackend, []*core.IPAddr{{Ip: "192.168.0.1", Port: 8080}})
		Expect(handler.SetForwardType(ipvs.DirectRoute)).To(Succeed())
		Expect(listDestinations(service)).To(ConsistOf(
			destination{Addr: "192.168.0.1:8080", Weight: 1, FwdType: ipvs.DirectRoute},
		))
		// same forward type is a no-op
		Expect(handler.SetForwardType(ipvs.DirectRoute)).To(Succeed())
	})

	It("should take over destinations already existing in ipvs", func() {
		handler.OnStartup(aBackend, []*core.IPAddr{{Ip: "192.168.0.1", Port: 8080}})

		// e.g. agent restarted
		newHandler := NewIpvsHandler(service, ipvs.Masquerade)
		Expect(newHandler.Clean()).To(Succeed())
		Expect(listDestinations(service)).To(BeEmpty())
	})
})
