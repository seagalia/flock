package vip_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	. "github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("VIP", func() {
	It("should be identified by its uuid", func() {
		aVip := &VIP{UUID: "vip-1"}
		Expect(aVip.GetIdentifier()).To(Equal("vip-1"))
		aVip.SetIdentifier("vip-2")
		Expect(aVip.GetUUID()).To(Equal("vip-2"))
		expectedSig, err := helpers.MessageSignature(aVip)
		Expect(err).ToNot(HaveOccurred())
		Expect(aVip.Signature()).To(Equal(expectedSig))
	})

	It("should tell the ip version", func() {
		Expect((&VIP{Ip: "10.0.0.1"}).IsIPV4()).To(BeTrue())
		Expect((&VIP{Ip: "fe80::1"}).IsIPV6()).To(BeTrue())
		var nilVip *VIP
		Expect(nilVip.IsIPV4()).To(BeFalse())
		Expect(nilVip.IsIPV6()).To(BeFalse())
	})

	DescribeTable("should tell a wildcard vip whatever its prefix length",
		func(ip string, prefixLength int32, expected bool) {
			aVip := &VIP{Ip: ip, PrefixLength: prefixLength}
			Expect(aVip.IsWildcard()).To(Equal(expected))
		},
		Entry("ipv4", "0.0.0.0", int32(32), true),
		Entry("ipv4 range", "0.0.0.0", int32(0), true),
		Entry("ipv6", "::", int32(128), true),
		Entry("concrete", "10.0.0.1", int32(32), false),
		Entry("no ip", "", int32(0), false),
	)

	DescribeTable("should give the vip network",
		func(ip string, prefixLength int32, expected string) {
			Expect((&VIP{Ip: ip, PrefixLength: prefixLength}).ToIpNet().String()).To(Equal(expected))
		},
		Entry("ipv4 without prefix", "10.0.0.1", int32(0), "10.0.0.1/32"),
		Entry("ipv4 with prefix", "10.0.0.1", int32(24), "10.0.0.0/24"),
		Entry("ipv4 with a too long prefix", "10.0.0.1", int32(64), "10.0.0.1/32"),
		Entry("ipv6 without prefix", "fe80::1", int32(0), "fe80::1/128"),
		Entry("ipv6 with prefix", "fe80::1", int32(64), "fe80::/64"),
	)

	DescribeTable("should default the prefix length to the one of a single address",
		func(aVip *VIP, expected int32) {
			aVip.ProvDefault()
			Expect(aVip.GetPrefixLength()).To(Equal(expected))
		},
		Entry("ipv4", &VIP{Ip: "10.0.0.1"}, int32(32)),
		Entry("ipv6", &VIP{Ip: "fe80::1"}, int32(128)),
		Entry("a prefix already set", &VIP{Ip: "10.0.0.1", PrefixLength: 24}, int32(24)),
		Entry("no ip yet", &VIP{}, int32(0)),
	)

	It("should not panic on defaulting a nil vip", func() {
		var nilVip *VIP
		Expect(nilVip.ProvDefault).NotTo(Panic())
	})

	It("should not panic on a nil vip network", func() {
		var nilVip *VIP
		Expect(nilVip.ToIpNet()).To(BeNil())
	})

	It("should tell if a list contains a vip by uuid or by network", func() {
		vips := VIPs{{UUID: "vip-1", Ip: "10.0.0.1"}}
		Expect(vips.Contains(&VIP{UUID: "vip-1"})).To(BeTrue())
		Expect(vips.Contains(&VIP{UUID: "vip-2"})).To(BeFalse())
		Expect(vips.ContainsByIpNet(&VIP{UUID: "vip-2", Ip: "10.0.0.1", PrefixLength: 32})).To(BeTrue())
		Expect(vips.ContainsByIpNet(&VIP{UUID: "vip-1", Ip: "10.0.0.2"})).To(BeFalse())
	})

	It("should give its starling agent announce", func() {
		aVip := &VIP{}
		announce, err := aVip.GetStarlingAnnounce()
		Expect(err).ToNot(HaveOccurred())
		Expect(announce.GetTypeAnnounce()).To(Equal(starling_announce.StarlingAnnounce_TypeAnnounce(0)))

		packed, err := anypb.New(&starling_announce.StarlingAnnounce{TypeAnnounce: starling_announce.StarlingAnnounce_LAYER2})
		Expect(err).ToNot(HaveOccurred())
		aVip.TypedAnnounce = packed
		announce, err = aVip.GetStarlingAnnounce()
		Expect(err).ToNot(HaveOccurred())
		Expect(announce.GetTypeAnnounce()).To(Equal(starling_announce.StarlingAnnounce_LAYER2))

		aVip.TypedAnnounce, err = anypb.New(wrapperspb.String("other"))
		Expect(err).ToNot(HaveOccurred())
		_, err = aVip.GetStarlingAnnounce()
		Expect(err).To(HaveOccurred())
	})

	It("should reference its virtual servers as foreign resources", func() {
		aVip := &VIP{VirtualServersUuid: []string{"vs-1"}}
		Expect(aVip.ForeignResources()).To(Equal(map[string][]string{helpers.TypeUrl[*vs.VirtualServer](): {"vs-1"}}))
	})

	It("should manage metadata", func() {
		aVip := &VIP{}
		Expect(aVip.DeleteMetadata("key")).To(BeNil())
		aVip.AddMetadata("key", "value")
		Expect(aVip.GetMetadata()).To(HaveKeyWithValue("key", "value"))
		Expect(aVip.DeleteMetadata("key")).To(BeEmpty())
		aVip.SetMetadata(map[string]string{"other": "value"})
		Expect(aVip.GetMetadata()).To(HaveKey("other"))
	})
})
