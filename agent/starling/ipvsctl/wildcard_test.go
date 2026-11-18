package ipvsctl_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/cloudflare/ipvs"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/agent/starling/ipvsctl"
)

type fakeMachineIPs struct {
	mutex sync.Mutex
	ipv4  []net.IP
	ipv6  []net.IP
}

func (f *fakeMachineIPs) Current(family int) []net.IP {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if family == syscall.AF_INET6 {
		return f.ipv6
	}
	return f.ipv4
}

func (f *fakeMachineIPs) Set(ips ...string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.ipv4, f.ipv6 = nil, nil
	for _, raw := range ips {
		ip := net.ParseIP(raw)
		if ip.To4() != nil {
			f.ipv4 = append(f.ipv4, ip.To4())
			continue
		}
		f.ipv6 = append(f.ipv6, ip)
	}
}

// failingDestinationsClient fails the first destination creations
type failingDestinationsClient struct {
	*FakeIpvsClient
	failures int
}

func (f *failingDestinationsClient) CreateDestination(svc ipvs.Service, dst ipvs.Destination) error {
	if f.failures > 0 {
		f.failures--
		return errors.New("netlink failed")
	}
	return f.FakeIpvsClient.CreateDestination(svc, dst)
}

// failingRemovalsClient fails the first service removals
type failingRemovalsClient struct {
	*FakeIpvsClient
	failures int
}

func (f *failingRemovalsClient) RemoveService(svc ipvs.Service) error {
	if f.failures > 0 {
		f.failures--
		return errors.New("netlink failed")
	}
	return f.FakeIpvsClient.RemoveService(svc)
}

var _ = Describe("WildcardClient", func() {
	var (
		ctx        context.Context
		kernel     *FakeIpvsClient
		machineIPs *fakeMachineIPs
		statePath  string
		client     *WildcardClient
		wildcard   ipvs.Service
		dest       ipvs.Destination
	)

	// kernelServices gives what the kernel holds, the copies and not the wildcard
	kernelServices := func() []string {
		GinkgoHelper()
		services, err := kernel.Services()
		Expect(err).ToNot(HaveOccurred())
		names := make([]string, 0, len(services))
		for _, svc := range services {
			names = append(names, ServiceToIpNet(svc.Service).String())
		}
		return names
	}

	kernelDestinations := func(cidr string) []string {
		GinkgoHelper()
		dests, err := kernel.Destinations(makeService(cidr, 80, ipvs.TCP))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		Expect(err).ToNot(HaveOccurred())
		names := make([]string, 0, len(dests))
		for _, dst := range dests {
			names = append(names, dst.Address.String())
		}
		return names
	}

	BeforeEach(func() {
		ctx = context.Background()
		kernel = NewFakeIpvsClient()
		machineIPs = &fakeMachineIPs{}
		machineIPs.Set("192.168.1.10", "192.168.2.10", "2001:db8::10")
		statePath = filepath.Join(GinkgoT().TempDir(), "flock", "copies.json")
		client = NewWildcardClient(kernel, machineIPs, statePath)
		wildcard = makeService("0.0.0.0/32", 80, ipvs.TCP)
		dest = makeDestination("10.1.0.1", 8080)
	})

	It("should copy a service on the unspecified address on every address of its family", func() {
		Expect(client.CreateService(wildcard)).To(Succeed())
		Expect(kernelServices()).To(ConsistOf("192.168.1.10/32", "192.168.2.10/32"))

		wildcard6 := makeService("::/128", 80, ipvs.TCP)
		Expect(client.CreateService(wildcard6)).To(Succeed())
		Expect(kernelServices()).To(ConsistOf("192.168.1.10/32", "192.168.2.10/32", "2001:db8::10/128"))
	})

	It("should list the copies as the wildcard alone", func() {
		concrete := makeService("10.0.0.1/32", 443, ipvs.TCP)
		Expect(client.CreateService(concrete)).To(Succeed())
		Expect(client.CreateService(wildcard)).To(Succeed())

		services, err := client.Services()
		Expect(err).ToNot(HaveOccurred())
		names := make([]string, 0, len(services))
		for _, svc := range services {
			names = append(names, ServiceToIpNet(svc.Service).String())
		}
		Expect(names).To(ConsistOf("0.0.0.0/32", "10.0.0.1/32"))

		listed, err := client.Service(wildcard)
		Expect(err).ToNot(HaveOccurred())
		Expect(listed.Port).To(Equal(uint16(80)))
	})

	It("should give every copy the destinations of the wildcard and their changes", func() {
		Expect(client.CreateService(wildcard)).To(Succeed())
		Expect(client.CreateDestination(wildcard, dest)).To(Succeed())
		Expect(kernelDestinations("192.168.1.10/32")).To(ConsistOf("10.1.0.1"))
		Expect(kernelDestinations("192.168.2.10/32")).To(ConsistOf("10.1.0.1"))

		dests, err := client.Destinations(wildcard)
		Expect(err).ToNot(HaveOccurred())
		Expect(dests).To(HaveLen(1))

		dest.Weight = 5
		Expect(client.UpdateDestination(wildcard, dest)).To(Succeed())
		copyDests, err := kernel.Destinations(makeService("192.168.2.10/32", 80, ipvs.TCP))
		Expect(err).ToNot(HaveOccurred())
		Expect(copyDests[0].Weight).To(Equal(uint32(5)))

		Expect(client.RemoveDestination(wildcard, dest)).To(Succeed())
		Expect(kernelDestinations("192.168.1.10/32")).To(BeEmpty())
		Expect(kernelDestinations("192.168.2.10/32")).To(BeEmpty())
	})

	It("should update and remove every copy with the wildcard", func() {
		Expect(client.CreateService(wildcard)).To(Succeed())
		wildcard.Scheduler = "lc"
		Expect(client.UpdateService(wildcard)).To(Succeed())
		copied, err := kernel.Service(makeService("192.168.1.10/32", 80, ipvs.TCP))
		Expect(err).ToNot(HaveOccurred())
		Expect(copied.Scheduler).To(Equal("lc"))

		Expect(client.RemoveService(wildcard)).To(Succeed())
		Expect(kernelServices()).To(BeEmpty())
		_, err = client.Service(wildcard)
		Expect(err).To(HaveOccurred())
	})

	It("should copy the wildcard with its destinations on an address added, and remove the copy of a gone one", func() {
		Expect(client.CreateService(wildcard)).To(Succeed())
		Expect(client.CreateDestination(wildcard, dest)).To(Succeed())

		machineIPs.Set("192.168.1.10", "192.168.2.10", "192.168.3.10")
		Expect(client.Sync(ctx)).To(Succeed())
		Expect(kernelServices()).To(ConsistOf("192.168.1.10/32", "192.168.2.10/32", "192.168.3.10/32"))
		Expect(kernelDestinations("192.168.3.10/32")).To(ConsistOf("10.1.0.1"))

		machineIPs.Set("192.168.3.10")
		Expect(client.Sync(ctx)).To(Succeed())
		Expect(kernelServices()).To(ConsistOf("192.168.3.10/32"))

		// the same addresses again change nothing
		Expect(client.Sync(ctx)).To(Succeed())
		Expect(kernelServices()).To(ConsistOf("192.168.3.10/32"))
	})

	It("should take over only the copies the state file lists from the run before", func() {
		Expect(client.CreateService(wildcard)).To(Succeed())
		Expect(client.CreateDestination(wildcard, makeDestination("10.9.9.9", 8080))).To(Succeed())
		concrete := makeService("192.168.3.10/32", 80, ipvs.TCP)
		Expect(kernel.CreateService(concrete)).To(Succeed())
		Expect(kernel.CreateDestination(concrete, makeDestination("10.3.3.3", 8080))).To(Succeed())

		// e.g. agent restarted, the kernel keeps the copies
		machineIPs.Set("192.168.1.10", "192.168.2.10", "192.168.3.10")
		restarted := NewWildcardClient(kernel, machineIPs, statePath)
		Expect(restarted.CreateService(wildcard)).To(Succeed())
		Expect(restarted.CreateDestination(wildcard, dest)).To(Succeed())
		Expect(kernelServices()).To(ConsistOf("192.168.1.10/32", "192.168.2.10/32", "192.168.3.10/32"))
		Expect(kernelDestinations("192.168.1.10/32")).To(ConsistOf("10.1.0.1"))
		Expect(kernelDestinations("192.168.3.10/32")).To(ConsistOf("10.3.3.3"), "a service it did not copy is left alone")
	})

	It("should record the copies in the state file, for the clean of a stopped agent", func() {
		Expect(client.CreateService(wildcard)).To(Succeed())
		Expect(client.CreateService(makeService("10.0.0.1/32", 443, ipvs.TCP))).To(Succeed())
		raw, err := os.ReadFile(statePath)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"192.168.1.10"`))
		Expect(string(raw)).To(ContainSubstring(`"192.168.2.10"`))
		Expect(string(raw)).ToNot(ContainSubstring("10.0.0.1"), "only the copies")

		SetClient(kernel)
		Expect(RemoveRecordedCopies(statePath)).To(Succeed())
		Expect(kernelServices()).To(ConsistOf("10.0.0.1/32"))
		Expect(statePath).ToNot(BeAnExistingFile())
	})

	It("should clean the recorded copies still there and skip the ones already gone", func() {
		Expect(client.CreateService(wildcard)).To(Succeed())
		Expect(kernel.RemoveService(makeService("192.168.1.10/32", 80, ipvs.TCP))).To(Succeed())

		SetClient(kernel)
		Expect(RemoveRecordedCopies(statePath)).To(Succeed())
		Expect(kernelServices()).To(BeEmpty())
	})

	It("should retry on the next sync the copies a removed wildcard failed to remove", func() {
		failing := &failingRemovalsClient{FakeIpvsClient: kernel, failures: 1}
		client = NewWildcardClient(failing, machineIPs, statePath)
		Expect(client.CreateService(wildcard)).To(Succeed())
		Expect(client.RemoveService(wildcard)).To(HaveOccurred())
		Expect(kernelServices()).To(HaveLen(1))
		raw, err := os.ReadFile(statePath)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"192.168.`), "still recorded for the offline clean")

		machineIPs.Set("192.168.1.10", "192.168.2.10", "192.168.3.10")
		Expect(client.Sync(ctx)).To(Succeed())
		Expect(kernelServices()).To(BeEmpty(), "the removed wildcard is copied on no new address")
	})

	It("should keep recording the copies of the run before that no wildcard claims again", func() {
		Expect(client.CreateService(wildcard)).To(Succeed())

		// e.g. the wildcard vip was deleted while the agent was stopped
		restarted := NewWildcardClient(kernel, machineIPs, statePath)
		Expect(restarted.CreateService(makeService("10.0.0.1/32", 443, ipvs.TCP))).To(Succeed())
		raw, err := os.ReadFile(statePath)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"192.168.1.10"`))

		SetClient(kernel)
		Expect(RemoveRecordedCopies(statePath)).To(Succeed())
		Expect(kernelServices()).To(ConsistOf("10.0.0.1/32"))
	})

	It("should clean nothing without a readable state file", func() {
		SetClient(kernel)
		Expect(RemoveRecordedCopies(statePath)).To(Succeed())
		Expect(os.MkdirAll(filepath.Dir(statePath), 0o755)).To(Succeed())
		Expect(os.WriteFile(statePath, []byte("not json"), 0o600)).To(Succeed())
		Expect(RemoveRecordedCopies(statePath)).To(Succeed())
	})

	It("should record no copy once the wildcard is removed", func() {
		Expect(client.CreateService(wildcard)).To(Succeed())
		Expect(client.RemoveService(wildcard)).To(Succeed())
		content, err := os.ReadFile(statePath)
		Expect(err).ToNot(HaveOccurred())
		var recorded []map[string]any
		Expect(json.Unmarshal(content, &recorded)).To(Succeed())
		Expect(recorded).To(BeEmpty())
	})

	It("should make again a copy removed by its address", func() {
		Expect(client.CreateService(wildcard)).To(Succeed())
		Expect(client.CreateDestination(wildcard, dest)).To(Succeed())
		// as a garbage collection listing from before the copy does
		Expect(client.RemoveService(makeService("192.168.1.10/32", 80, ipvs.TCP))).To(Succeed())
		Expect(kernelServices()).To(ConsistOf("192.168.1.10/32", "192.168.2.10/32"))
		Expect(kernelDestinations("192.168.1.10/32")).To(ConsistOf("10.1.0.1"))
	})

	It("should give a copy the destination a failed write missed on the next sync", func() {
		failing := &failingDestinationsClient{FakeIpvsClient: kernel, failures: 1}
		client = NewWildcardClient(failing, machineIPs, statePath)
		Expect(client.CreateService(wildcard)).To(Succeed())
		Expect(client.CreateDestination(wildcard, dest)).To(HaveOccurred())

		Expect(client.Sync(ctx)).To(Succeed())
		Expect(kernelDestinations("192.168.1.10/32")).To(ConsistOf("10.1.0.1"))
		Expect(kernelDestinations("192.168.2.10/32")).To(ConsistOf("10.1.0.1"))
	})

	It("should not sync once its ctx is done", func() {
		Expect(client.CreateService(wildcard)).To(Succeed())
		machineIPs.Set("192.168.3.10")
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		Expect(client.Sync(canceled)).To(Succeed())
		Expect(kernelServices()).To(ConsistOf("192.168.1.10/32", "192.168.2.10/32"))
	})

	Context("with a concrete service on one of the addresses", func() {
		var concrete ipvs.Service

		BeforeEach(func() {
			concrete = makeService("192.168.1.10/32", 80, ipvs.TCP)
		})

		It("should leave the address to the concrete service already there", func() {
			Expect(client.CreateService(concrete)).To(Succeed())
			Expect(client.CreateDestination(concrete, makeDestination("10.2.0.1", 8080))).To(Succeed())
			Expect(client.CreateService(wildcard)).To(Succeed())
			Expect(client.CreateDestination(wildcard, dest)).To(Succeed())

			Expect(kernelServices()).To(ConsistOf("192.168.1.10/32", "192.168.2.10/32"))
			Expect(kernelDestinations("192.168.1.10/32")).To(ConsistOf("10.2.0.1"))
		})

		It("should give the address to a concrete service coming later, and take it back once it is gone", func() {
			Expect(client.CreateService(wildcard)).To(Succeed())
			Expect(client.CreateDestination(wildcard, dest)).To(Succeed())

			Expect(client.CreateService(concrete)).To(Succeed())
			Expect(client.CreateDestination(concrete, makeDestination("10.2.0.1", 8080))).To(Succeed())
			Expect(kernelDestinations("192.168.1.10/32")).To(ConsistOf("10.2.0.1"))

			// a change of the addresses keeps the address with the concrete service
			Expect(client.Sync(ctx)).To(Succeed())
			Expect(kernelDestinations("192.168.1.10/32")).To(ConsistOf("10.2.0.1"))

			Expect(client.RemoveService(concrete)).To(Succeed())
			Expect(kernelServices()).To(ConsistOf("192.168.1.10/32", "192.168.2.10/32"))
			Expect(kernelDestinations("192.168.1.10/32")).To(ConsistOf("10.1.0.1"))
		})

		It("should keep the copy beside a concrete service on another port or protocol", func() {
			Expect(client.CreateService(makeService("192.168.1.10/32", 443, ipvs.TCP))).To(Succeed())
			Expect(client.CreateService(makeService("192.168.1.10/32", 80, ipvs.UDP))).To(Succeed())
			Expect(client.CreateService(wildcard)).To(Succeed())
			Expect(kernelServices()).To(HaveLen(4))
		})
	})
})
