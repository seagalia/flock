package ipams

import (
	"net/netip"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
)

var _ = Describe("Zone intervals", func() {
	interval := func(start, end string) IPInterval {
		return IPInterval{Start: netip.MustParseAddr(start), End: netip.MustParseAddr(end)}
	}
	pooled := func(i IPInterval, pool string) IPInterval {
		i.Pool = pool
		return i
	}

	It("should keep network and broadcast addresses out of ipv4 subnets only", func() {
		intervals, err := intervalsFromZone(&zone.Zone{Pools: []*zone.IpPool{{Subnets: []*zone.Subnet{
			{Ip: "10.0.0.7", PrefixLength: 24},
			{Ip: "10.1.0.1", PrefixLength: 32},
			{Ip: "2001:db8::", PrefixLength: 127},
		}}}})
		Expect(err).ToNot(HaveOccurred())
		Expect(intervals).To(Equal([]IPInterval{
			interval("10.0.0.1", "10.0.0.254"),
			interval("10.1.0.1", "10.1.0.1"),
			interval("2001:db8::", "2001:db8::1"),
		}))
	})

	It("should give one interval per subnet and ip range, with its pool", func() {
		intervals, err := intervalsFromZone(&zone.Zone{Pools: []*zone.IpPool{
			{Name: "a", IpRanges: []*zone.IpRange{
				{IpMin: "10.0.0.20", IpMax: "10.0.0.30"},
				{IpMin: "10.0.0.10", IpMax: "10.0.0.19"},
			}},
			{Name: "b", Subnets: []*zone.Subnet{{Ip: "10.1.0.0", PrefixLength: 30}}},
		}})
		Expect(err).ToNot(HaveOccurred())
		Expect(intervals).To(Equal([]IPInterval{
			pooled(interval("10.0.0.20", "10.0.0.30"), "a"),
			pooled(interval("10.0.0.10", "10.0.0.19"), "a"),
			pooled(interval("10.1.0.1", "10.1.0.2"), "b"),
		}))
	})

	It("should refuse inverted or mixed family ranges", func() {
		_, err := intervalsFromZone(&zone.Zone{Pools: []*zone.IpPool{{IpRanges: []*zone.IpRange{{IpMin: "10.0.0.9", IpMax: "10.0.0.1"}}}}})
		Expect(err).To(MatchError(ContainSubstring("invalid IP range")))
		_, err = intervalsFromZone(&zone.Zone{Pools: []*zone.IpPool{{IpRanges: []*zone.IpRange{{IpMin: "10.0.0.1", IpMax: "2001:db8::1"}}}}})
		Expect(err).To(MatchError(ContainSubstring("invalid IP range")))
	})

})

var _ = Describe("ValidateZone", func() {
	rangePool := func(name, min, max string) *zone.IpPool {
		return &zone.IpPool{Name: name, IpRanges: []*zone.IpRange{{IpMin: min, IpMax: max}}}
	}
	subnetPool := func(name, ip string, prefixLength int32) *zone.IpPool {
		return &zone.IpPool{Name: name, Subnets: []*zone.Subnet{{Ip: ip, PrefixLength: prefixLength}}}
	}

	It("should refuse duplicate pool names", func() {
		err := ValidateZone(&zone.Zone{Pools: []*zone.IpPool{
			rangePool("a", "10.0.0.1", "10.0.0.2"),
			rangePool("a", "10.0.1.1", "10.0.1.2"),
		}})
		Expect(err).To(MatchError(ContainSubstring(`"a"`)))
	})

	It("should refuse intervals of two pools overlapping", func() {
		err := ValidateZone(&zone.Zone{Pools: []*zone.IpPool{
			subnetPool("a", "10.0.0.0", 24),
			rangePool("b", "10.0.0.10", "10.0.0.20"),
		}})
		Expect(err).To(MatchError(And(ContainSubstring(`"a"`), ContainSubstring(`"b"`))))
	})

	It("should refuse intervals of one pool overlapping", func() {
		pool := subnetPool("a", "10.0.0.0", 24)
		pool.IpRanges = []*zone.IpRange{{IpMin: "10.0.0.250", IpMax: "10.0.1.5"}}
		err := ValidateZone(&zone.Zone{Pools: []*zone.IpPool{pool}})
		Expect(err).To(MatchError(ContainSubstring(`"a"`)))
	})

	It("should accept adjacent intervals", func() {
		Expect(ValidateZone(&zone.Zone{Pools: []*zone.IpPool{
			rangePool("a", "10.0.0.0", "10.0.0.255"),
			rangePool("b", "10.0.1.0", "10.0.1.10"),
		}})).To(Succeed())
	})

	It("should compare intervals of the same family only", func() {
		Expect(ValidateZone(&zone.Zone{Pools: []*zone.IpPool{
			subnetPool("a", "10.0.0.0", 24),
			subnetPool("b", "::a00:0", 120),
		}})).To(Succeed())
	})

	It("should accept a zone without pools", func() {
		Expect(ValidateZone(&zone.Zone{})).To(Succeed())
	})
})
