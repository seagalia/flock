package handlers_test

import (
	"context"
	"strings"
	"sync/atomic"
	"time"

	"github.com/seagalia/flock/sdk/agentsdk/stores"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/f5/handlers"
	"github.com/seagalia/flock/sdk/agentsdk/discoverers"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
)

const backendUUID = "33333333-3333-3333-3333-333333333333"

func staticBackend(ips ...string) *backend.Backend {
	return &backend.Backend{UUID: backendUUID, Name: "b", Endpoint: &backend.Backend_IpAddrGroup{
		IpAddrGroup: &core.IPAddrGroup{IpAddresses: addrsOf(ips)},
	}}
}

func edsBackend(ips ...string) *backend.Backend {
	return &backend.Backend{UUID: backendUUID, Name: "b", Endpoint: &backend.Backend_Eds{
		Eds: &backend.EDS{ServiceName: strings.Join(ips, ",")},
	}}
}

func fakeEds(b *backend.Backend) (discoverers.Discoverer, error) {
	return discoverers.NewStatic(addrsOf(strings.Split(b.GetEds().GetServiceName(), ","))), nil
}

// slowStopDiscoverer takes a while to return once its ctx ends, as a discovery finishing a call
type slowStopDiscoverer struct {
	returned *atomic.Bool
}

func (d slowStopDiscoverer) Run(ctx context.Context, _ chan<- []*core.IPAddr) error {
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)
	d.returned.Store(true)
	return nil
}

func addrsOf(ips []string) []*core.IPAddr {
	addrs := make([]*core.IPAddr, 0, len(ips))
	for _, ip := range ips {
		addrs = append(addrs, &core.IPAddr{Ip: ip, Port: 8080})
	}
	return addrs
}

func ipsOf(h *handlers.BackendHandler) []string {
	endpoints, _ := h.Endpoints(backendUUID)
	ips := make([]string, 0, len(endpoints))
	for _, ep := range endpoints {
		ips = append(ips, ep.GetIp())
	}
	return ips
}

var _ = Describe("Change handler", func() {
	It("notifies on add and on remove", func() {
		var calls atomic.Int32
		h := handlers.NewChangeHandler[*vip.VIP]("vip", func() { calls.Add(1) })

		Expect(h.OnMessageAdded(context.Background(), &vip.VIP{UUID: "v"})).To(Succeed())
		Expect(h.OnMessageRemove(context.Background(), &vip.VIP{UUID: "v"})).To(Succeed())

		Expect(calls.Load()).To(BeEquivalentTo(2))
		Expect(h.Name()).To(Equal("vip"))
	})
})

var _ = Describe("Backend handler", func() {
	var (
		calls atomic.Int32
		h     *handlers.BackendHandler
		ctx   = context.Background()
	)

	BeforeEach(func() {
		calls.Store(0)
		h = handlers.NewBackendHandler(func() { calls.Add(1) }, stores.NewStateStore())
		h.SetDiscoverers(fakeEds)
		DeferCleanup(h.Stop)
	})

	It("waits on stop for the discoveries it runs", func() {
		var returned atomic.Bool
		h.SetDiscoverers(func(*backend.Backend) (discoverers.Discoverer, error) {
			return slowStopDiscoverer{returned: &returned}, nil
		})
		Expect(h.OnMessageAdded(ctx, edsBackend("10.0.0.1"))).To(Succeed())

		Expect(h.Stop()).To(Succeed())

		Expect(returned.Load()).To(BeTrue())
	})

	It("has no endpoints for a backend it doesn't know", func() {
		_, discovered := h.Endpoints(backendUUID)

		Expect(discovered).To(BeFalse())
	})

	It("discovers the endpoints of an EDS backend", func() {
		Expect(h.OnMessageAdded(ctx, edsBackend("10.0.0.1", "10.0.0.2"))).To(Succeed())

		Eventually(func() []string { return ipsOf(h) }).Should(ConsistOf("10.0.0.1", "10.0.0.2"))
		_, discovered := h.Endpoints(backendUUID)
		Expect(discovered).To(BeTrue())
		Eventually(calls.Load).Should(BeEquivalentTo(1))
	})

	It("discovers again when the backend changes", func() {
		Expect(h.OnMessageAdded(ctx, edsBackend("10.0.0.1"))).To(Succeed())
		Eventually(calls.Load).Should(BeEquivalentTo(1))

		Expect(h.OnMessageAdded(ctx, edsBackend("10.0.0.3"))).To(Succeed())

		Eventually(func() []string { return ipsOf(h) }).Should(ConsistOf("10.0.0.3"))
		Eventually(calls.Load).Should(BeEquivalentTo(3), "the update, then the endpoints it found")
	})

	It("notifies on an update that keeps the same discovery", func() {
		Expect(h.OnMessageAdded(ctx, edsBackend("10.0.0.1"))).To(Succeed())
		Eventually(calls.Load).Should(BeEquivalentTo(1))

		b := edsBackend("10.0.0.1")
		b.LbPolicy = backend.Backend_LEAST_REQUEST
		Expect(h.OnMessageAdded(ctx, b)).To(Succeed())

		Eventually(calls.Load).Should(BeEquivalentTo(2))
		Consistently(calls.Load, 200*time.Millisecond).Should(BeEquivalentTo(2))
	})

	It("leaves a static backend to the BIG-IP and notifies at once", func() {
		Expect(h.OnMessageAdded(ctx, staticBackend("10.0.0.1"))).To(Succeed())

		Expect(calls.Load()).To(BeEquivalentTo(1))
		_, discovered := h.Endpoints(backendUUID)
		Expect(discovered).To(BeFalse())
	})

	It("stops discovering a backend the BIG-IP resolves from now on", func() {
		Expect(h.OnMessageAdded(ctx, edsBackend("10.0.0.1"))).To(Succeed())
		Eventually(calls.Load).Should(BeEquivalentTo(1))

		Expect(h.OnMessageAdded(ctx, &backend.Backend{UUID: backendUUID, Name: "b", Endpoint: &backend.Backend_Dns{
			Dns: &backend.DNS{Host: "app.example.com", Port: 8080},
		}})).To(Succeed())

		Expect(calls.Load()).To(BeEquivalentTo(2))
		_, discovered := h.Endpoints(backendUUID)
		Expect(discovered).To(BeFalse())
	})

	It("forgets a removed backend", func() {
		Expect(h.OnMessageAdded(ctx, edsBackend("10.0.0.1"))).To(Succeed())
		Eventually(calls.Load).Should(BeEquivalentTo(1))

		Expect(h.OnMessageRemove(ctx, edsBackend("10.0.0.1"))).To(Succeed())

		_, discovered := h.Endpoints(backendUUID)
		Expect(discovered).To(BeFalse())
		Expect(calls.Load()).To(BeEquivalentTo(2))
	})
})
