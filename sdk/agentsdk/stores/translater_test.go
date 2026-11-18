package stores_test

import (
	"context"
	"github.com/seagalia/flock/sdk/storers"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
)

var _ = Describe("IpPortTranslator", func() {
	var (
		translator *IpPortTranslator
		ctx        = context.Background()
	)

	BeforeEach(func() {
		store := storers.NewMemoryManager()
		translator = NewIpPortTranslator(store)
		Expect(store.VirtualServers().Create(context.Background(), tcpVs("vs-tcp", 443, "backend-1"))).To(Succeed())
		Expect(store.VirtualServers().Create(context.Background(), udpVs("vs-udp", 53, "backend-1"))).To(Succeed())
	})

	It("should find virtual servers by vip ip, port and protocol", func() {
		aVip := &vip.VIP{UUID: "vip-1", Ip: "10.0.0.1", VirtualServersUuid: []string{"vs-tcp", "vs-udp"}}
		Expect(translator.OnMessageAdded(ctx, aVip)).To(Succeed())

		found, ok := translator.FindVs("10.0.0.1", 443, "tcp")
		Expect(ok).To(BeTrue())
		Expect(found.GetUUID()).To(Equal("vs-tcp"))
		found, ok = translator.FindVs("10.0.0.1", 53, "udp")
		Expect(ok).To(BeTrue())
		Expect(found.GetUUID()).To(Equal("vs-udp"))

		_, ok = translator.FindVs("10.0.0.1", 443, "udp")
		Expect(ok).To(BeFalse())
	})

	It("should forget virtual servers not attached anymore to the vip", func() {
		aVip := &vip.VIP{UUID: "vip-1", Ip: "10.0.0.1", VirtualServersUuid: []string{"vs-tcp", "vs-udp"}}
		Expect(translator.OnMessageAdded(ctx, aVip)).To(Succeed())

		aVip.VirtualServersUuid = []string{"vs-udp"}
		Expect(translator.OnMessageAdded(ctx, aVip)).To(Succeed())
		_, ok := translator.FindVs("10.0.0.1", 443, "tcp")
		Expect(ok).To(BeFalse())
		_, ok = translator.FindVs("10.0.0.1", 53, "udp")
		Expect(ok).To(BeTrue())
	})

	It("should forget every virtual servers of a removed vip only", func() {
		Expect(translator.OnMessageAdded(ctx, &vip.VIP{Ip: "10.0.0.1", VirtualServersUuid: []string{"vs-tcp"}})).To(Succeed())
		Expect(translator.OnMessageAdded(ctx, &vip.VIP{Ip: "10.0.0.10", VirtualServersUuid: []string{"vs-tcp"}})).To(Succeed())

		Expect(translator.OnMessageRemove(ctx, &vip.VIP{Ip: "10.0.0.1"})).To(Succeed())
		_, ok := translator.FindVs("10.0.0.1", 443, "tcp")
		Expect(ok).To(BeFalse())
		_, ok = translator.FindVs("10.0.0.10", 443, "tcp")
		Expect(ok).To(BeTrue())
	})

	It("should keep ipv6 vips apart when one address is a prefix of another", func() {
		Expect(translator.OnMessageAdded(ctx, &vip.VIP{Ip: "fe80::1", VirtualServersUuid: []string{"vs-tcp"}})).To(Succeed())
		Expect(translator.OnMessageAdded(ctx, &vip.VIP{Ip: "fe80::1:5", VirtualServersUuid: []string{"vs-tcp"}})).To(Succeed())

		Expect(translator.OnMessageRemove(ctx, &vip.VIP{Ip: "fe80::1"})).To(Succeed())
		_, ok := translator.FindVs("fe80::1", 443, "tcp")
		Expect(ok).To(BeFalse())
		_, ok = translator.FindVs("fe80::1:5", 443, "tcp")
		Expect(ok).To(BeTrue())
	})

	It("should fail when a virtual server of the vip can't be found", func() {
		aVip := &vip.VIP{Ip: "10.0.0.1", VirtualServersUuid: []string{"missing"}}
		Expect(translator.OnMessageAdded(ctx, aVip)).To(MatchError(ContainSubstring("missing")))
	})

})
