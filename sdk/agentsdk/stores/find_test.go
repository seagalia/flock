package stores_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"
)

func uuidsOf[T interface{ GetUUID() string }](messages []T) []string {
	result := make([]string, 0, len(messages))
	for _, message := range messages {
		result = append(result, message.GetUUID())
	}
	return result
}

var _ = Describe("Finding related resources", func() {
	var sm storers.StoreManager
	ctx := context.Background()

	BeforeEach(func() {
		sm = storers.NewMemoryManager()
		Expect(sm.Backends().Create(context.Background(), &backend.Backend{UUID: "backend-1"})).To(Succeed())
		Expect(sm.Backends().Create(context.Background(), &backend.Backend{UUID: "backend-2"})).To(Succeed())
		Expect(sm.VirtualServers().Create(context.Background(), httpVs("vs-http", map[string]string{"a.local": "backend-1", "b.local": "backend-2"}))).To(Succeed())
		Expect(sm.VirtualServers().Create(context.Background(), tcpVs("vs-tcp", 443, "backend-1"))).To(Succeed())
		Expect(sm.VIPs().Create(context.Background(), &vip.VIP{UUID: "vip-1", VirtualServersUuid: []string{"vs-http", "vs-tcp"}})).To(Succeed())
		Expect(sm.VIPs().Create(context.Background(), &vip.VIP{UUID: "vip-2", VirtualServersUuid: []string{"vs-tcp"}})).To(Succeed())
	})

	It("should find vips of a virtual server", func() {
		vips, err := FindVipsForVirtualServer(context.Background(), sm, "vs-tcp")
		Expect(err).ToNot(HaveOccurred())
		Expect(uuidsOf(vips)).To(ConsistOf("vip-1", "vip-2"))

		_, err = FindVipsForVirtualServer(context.Background(), sm, "unknown")
		Expect(err).To(MatchError(ContainSubstring("no vip found")))

		vips, err = RetryFindVipsForVirtualServer(ctx, sm, "vs-http")
		Expect(err).ToNot(HaveOccurred())
		Expect(uuidsOf(vips)).To(ConsistOf("vip-1"))
	})

	It("should read resources by uuid with retry", func() {
		Expect(RetryFindVirtualServer(ctx, sm, "vs-tcp")).To(HaveField("UUID", "vs-tcp"))
		Expect(RetryFindVip(ctx, sm, "vip-1")).To(HaveField("UUID", "vip-1"))
		Expect(RetryFindBackend(ctx, sm, "backend-1")).To(HaveField("UUID", "backend-1"))
	})

	It("should fail after retries when a resource stays missing", func() {
		_, err := RetryFindBackend(ctx, sm, "missing")
		Expect(err).To(MatchError(ContainSubstring("failed to retrieve backend missing")))
	})

	It("should stop retrying when the context ends", func() {
		cancelled, cancel := context.WithCancel(ctx)
		time.AfterFunc(100*time.Millisecond, cancel)
		started := time.Now()

		_, err := RetryFindBackend(cancelled, sm, "missing")
		Expect(err).To(MatchError(context.Canceled))
		Expect(time.Since(started)).To(BeNumerically("<", 900*time.Millisecond))
	})

	It("should find resources by type url in a store manager", func() {
		found, err := FindByTypeAndUuidByStore(context.Background(), sm, helpers.TypeUrl[*vs.VirtualServer](), "vs-tcp")
		Expect(err).ToNot(HaveOccurred())
		Expect(helpers.GetIdentifier(found)).To(Equal("vs-tcp"))

		all, err := FindAllByTypeForStore(context.Background(), sm, helpers.TypeUrl[*backend.Backend]())
		Expect(err).ToNot(HaveOccurred())
		Expect(all).To(HaveLen(2))

		for _, typeUrl := range []string{
			helpers.TypeUrl[*vip.VIP](), helpers.TypeUrl[*vs.VirtualServer](),
		} {
			all, err = FindAllByTypeForStore(context.Background(), sm, typeUrl)
			Expect(err).ToNot(HaveOccurred())
			Expect(all).To(HaveLen(2), typeUrl)
		}

		_, err = FindByTypeAndUuidByStore(context.Background(), sm, "unknown", "id")
		Expect(err).To(MatchError(ContainSubstring("no store found")))
		_, err = FindAllByTypeForStore(context.Background(), sm, "unknown")
		Expect(err).To(MatchError(ContainSubstring("no store found")))
	})
})
