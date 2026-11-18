package ipvsctl_test

import (
	"github.com/cloudflare/ipvs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"

	. "github.com/seagalia/flock/agent/starling/ipvsctl"
)

var _ = Describe("FakeIpvsClient", func() {
	var (
		fakeClient *FakeIpvsClient
		service    ipvs.Service
	)

	BeforeEach(func() {
		fakeClient = NewFakeIpvsClient()
		service = makeService("10.0.0.1/32", 80, ipvs.TCP)
	})

	It("should give info", func() {
		info, err := fakeClient.Info()
		Expect(err).ToNot(HaveOccurred())
		Expect(info.ConnectionTableSize).To(BeNumerically(">", 0))
	})

	Context("Services", func() {
		It("should create, read, update and remove a service", func() {
			Expect(fakeClient.CreateService(service)).To(Succeed())

			svc, err := fakeClient.Service(service)
			Expect(err).ToNot(HaveOccurred())
			Expect(svc.Service).To(Equal(service))

			services, err := fakeClient.Services()
			Expect(err).ToNot(HaveOccurred())
			Expect(services).To(HaveLen(1))

			service.Scheduler = "wrr"
			Expect(fakeClient.UpdateService(service)).To(Succeed())
			svc, err = fakeClient.Service(service)
			Expect(err).ToNot(HaveOccurred())
			Expect(svc.Service.Scheduler).To(Equal("wrr"))

			Expect(fakeClient.RemoveService(service)).To(Succeed())
			services, err = fakeClient.Services()
			Expect(err).ToNot(HaveOccurred())
			Expect(services).To(BeEmpty())
		})

		It("should consider services with different protocol as different", func() {
			Expect(fakeClient.CreateService(service)).To(Succeed())
			Expect(fakeClient.CreateService(makeService("10.0.0.1/32", 80, ipvs.UDP))).To(Succeed())
			services, err := fakeClient.Services()
			Expect(err).ToNot(HaveOccurred())
			Expect(services).To(HaveLen(2))
		})

		It("should fail to create an existing service", func() {
			Expect(fakeClient.CreateService(service)).To(Succeed())
			Expect(fakeClient.CreateService(service)).To(MatchError(ContainSubstring("service already exists")))
		})

		It("should fail on unknown service", func() {
			_, err := fakeClient.Service(service)
			Expect(err).To(MatchError(ContainSubstring("service not found")))
			Expect(fakeClient.UpdateService(service)).To(MatchError(ContainSubstring("service not found")))
			Expect(fakeClient.RemoveService(service)).To(MatchError(ContainSubstring("service not found")))
		})
	})

	Context("Destinations", func() {
		var dest ipvs.Destination

		BeforeEach(func() {
			Expect(fakeClient.CreateService(service)).To(Succeed())
			dest = makeDestination("192.168.0.1", 8080)
		})

		It("should create, update and remove a destination", func() {
			Expect(fakeClient.CreateDestination(service, dest)).To(Succeed())

			dests, err := fakeClient.Destinations(service)
			Expect(err).ToNot(HaveOccurred())
			Expect(dests).To(HaveLen(1))
			Expect(dests[0].Destination).To(Equal(dest))

			dest.Weight = 10
			Expect(fakeClient.UpdateDestination(service, dest)).To(Succeed())
			dests, err = fakeClient.Destinations(service)
			Expect(err).ToNot(HaveOccurred())
			Expect(dests[0].Weight).To(Equal(uint32(10)))

			Expect(fakeClient.RemoveDestination(service, dest)).To(Succeed())
			_, err = fakeClient.Destinations(service)
			Expect(err).To(MatchError(os.ErrNotExist))
		})

		It("should only list destinations of the given service", func() {
			otherService := makeService("10.0.0.2/32", 80, ipvs.TCP)
			Expect(fakeClient.CreateService(otherService)).To(Succeed())
			Expect(fakeClient.CreateDestination(service, dest)).To(Succeed())
			Expect(fakeClient.CreateDestination(otherService, makeDestination("192.168.0.2", 8080))).To(Succeed())

			dests, err := fakeClient.Destinations(service)
			Expect(err).ToNot(HaveOccurred())
			Expect(dests).To(HaveLen(1))
			Expect(dests[0].Address.String()).To(Equal("192.168.0.1"))
		})

		It("should fail to create an existing destination", func() {
			Expect(fakeClient.CreateDestination(service, dest)).To(Succeed())
			Expect(fakeClient.CreateDestination(service, dest)).To(MatchError(ContainSubstring("destination already exists")))
		})

		It("should fail on unknown destination", func() {
			Expect(fakeClient.UpdateDestination(service, dest)).To(MatchError(ContainSubstring("destination not found")))
			Expect(fakeClient.RemoveDestination(service, dest)).To(MatchError(ContainSubstring("destination not found")))
		})
	})
})
