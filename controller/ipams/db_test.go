package ipams_test

import (
	"context"
	"net/netip"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/dbs"
	. "github.com/seagalia/flock/controller/ipams"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
)

// one db manager for the suite: it registers prometheus collectors globally
var dbMan *dbs.DbManager

var _ = BeforeSuite(func() {
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	var err error
	dbMan, err = dbs.NewDbManager(ctx, dbs.DbTypeSqlite, filepath.Join(GinkgoT().TempDir(), "ipam.db"), time.Hour, time.Hour, 1, 1)
	Expect(err).ToNot(HaveOccurred())
})

var _ = Describe("Db", func() {
	var (
		ctx   context.Context
		ipam  *Db
		aZone *zone.Zone
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		ipam, err = NewDb(context.Background(), dbMan)
		Expect(err).ToNot(HaveOccurred())
		for _, table := range []string{TableIPAMRecycled, TableIPAMAllocation, TableIPAMRange, TableIPAMPool} {
			_, err := dbMan.Exec(context.Background(), dbs.MakeDbQuery("DELETE FROM "+table, nil))
			Expect(err).ToNot(HaveOccurred())
		}
		aZone = &zone.Zone{
			UUID:  "zone-1",
			Name:  "production",
			Pools: []*zone.IpPool{{Name: "default", Subnets: []*zone.Subnet{{Ip: "10.0.0.0", PrefixLength: 30}}}},
		}
	})

	It("should start again on existing tables", func() {
		Expect(ipam.AcquireSpecificIP(ctx, aZone, netip.MustParseAddr("10.0.0.1"), "vip-1")).To(Succeed())

		restarted, err := NewDb(context.Background(), dbMan)
		Expect(err).ToNot(HaveOccurred())

		Expect(restarted.HasAllocatedIPs(ctx, aZone)).To(BeTrue())
	})

	It("should acquire the ips of a subnet in order without network and broadcast addresses", func() {
		first, err := ipam.AcquireIP(ctx, aZone, "default", "vip-1")
		Expect(err).ToNot(HaveOccurred())
		second, err := ipam.AcquireIP(ctx, aZone, "default", "vip-2")
		Expect(err).ToNot(HaveOccurred())
		Expect([]string{first.String(), second.String()}).To(Equal([]string{"10.0.0.1", "10.0.0.2"}))

		_, err = ipam.AcquireIP(ctx, aZone, "default", "vip-3")
		Expect(err).To(MatchError(ErrPoolExhausted))
	})

	It("should skip an ip acquired explicitly", func() {
		Expect(ipam.AcquireSpecificIP(ctx, aZone, netip.MustParseAddr("10.0.0.1"), "vip-1")).To(Succeed())

		next, err := ipam.AcquireIP(ctx, aZone, "default", "vip-2")

		Expect(err).ToNot(HaveOccurred())
		Expect(next.String()).To(Equal("10.0.0.2"))
	})

	It("should acquire an ip again for its owner only", func() {
		ip := netip.MustParseAddr("10.0.0.1")
		Expect(ipam.AcquireSpecificIP(ctx, aZone, ip, "vip-1")).To(Succeed())

		Expect(ipam.AcquireSpecificIP(ctx, aZone, ip, "vip-1")).To(Succeed())
		Expect(ipam.AcquireSpecificIP(ctx, aZone, ip, "vip-2")).To(MatchError(ErrIPAllocated))
	})

	It("should refuse an ip outside of the zone", func() {
		err := ipam.AcquireSpecificIP(ctx, aZone, netip.MustParseAddr("192.168.0.1"), "vip-1")
		Expect(err).To(MatchError(ErrIPOutOfRange))
	})

	It("should keep the same ip apart in each zone", func() {
		otherZone := &zone.Zone{UUID: "zone-2", Name: "lyon", Pools: aZone.GetPools()}
		ip := netip.MustParseAddr("10.0.0.1")

		Expect(ipam.AcquireSpecificIP(ctx, aZone, ip, "vip-1")).To(Succeed())
		Expect(ipam.AcquireSpecificIP(ctx, otherZone, ip, "vip-1")).To(Succeed())
		Expect(ipam.ReleaseIP(ctx, aZone, ip)).To(Succeed())

		Expect(ipam.HasAllocatedIPs(ctx, aZone)).To(BeFalse())
		Expect(ipam.HasAllocatedIPs(ctx, otherZone)).To(BeTrue())
	})

	It("should give a released ip again", func() {
		_, err := ipam.AcquireIP(ctx, aZone, "default", "vip-1")
		Expect(err).ToNot(HaveOccurred())
		_, err = ipam.AcquireIP(ctx, aZone, "default", "vip-2")
		Expect(err).ToNot(HaveOccurred())

		Expect(ipam.ReleaseIP(ctx, aZone, netip.MustParseAddr("10.0.0.1"))).To(Succeed())
		again, err := ipam.AcquireIP(ctx, aZone, "default", "vip-3")

		Expect(err).ToNot(HaveOccurred())
		Expect(again.String()).To(Equal("10.0.0.1"))
	})

	It("should tell an ip that is not allocated", func() {
		err := ipam.ReleaseIP(ctx, aZone, netip.MustParseAddr("10.0.0.1"))
		Expect(err).To(MatchError(ErrIPNotAllocated))
	})

	It("should release every ip of an owner, in all zones", func() {
		otherZone := &zone.Zone{UUID: "zone-2", Name: "lyon", Pools: []*zone.IpPool{{Name: "default", IpRanges: []*zone.IpRange{{IpMin: "10.9.0.10", IpMax: "10.9.0.20"}}}}}
		_, err := ipam.AcquireIP(ctx, aZone, "default", "vip-1")
		Expect(err).ToNot(HaveOccurred())
		_, err = ipam.AcquireIP(ctx, otherZone, "default", "vip-1")
		Expect(err).ToNot(HaveOccurred())
		_, err = ipam.AcquireIP(ctx, aZone, "default", "vip-2")
		Expect(err).ToNot(HaveOccurred())

		Expect(ipam.ReleaseOwner(ctx, "vip-1")).To(Succeed())

		Expect(ipam.HasAllocatedIPs(ctx, otherZone)).To(BeFalse())
		Expect(ipam.HasAllocatedIPs(ctx, aZone)).To(BeTrue(), "vip-2 keeps its ip")
		Expect(ipam.ReleaseOwner(ctx, "unknown")).To(Succeed())
	})

	It("should keep its pool when the zone changes but not its ips", func() {
		_, err := ipam.AcquireIP(ctx, aZone, "default", "vip-1")
		Expect(err).ToNot(HaveOccurred())

		aZone.Name = "renamed"
		aZone.Metadata = map[string]string{"site": "paris"}
		_, err = ipam.AcquireIP(ctx, aZone, "default", "vip-2")
		Expect(err).ToNot(HaveOccurred())
	})

	It("should manage nothing in a zone without subnets nor ip ranges", func() {
		public := &zone.Zone{UUID: "public", Name: "public"}

		_, err := ipam.AcquireIP(ctx, public, "default", "vip-1")
		Expect(err).To(HaveOccurred())
		Expect(ipam.AcquireSpecificIP(ctx, public, netip.MustParseAddr("1.2.3.4"), "vip-1")).To(Succeed())
		Expect(ipam.ReleaseIP(ctx, public, netip.MustParseAddr("1.2.3.4"))).To(Succeed())
		Expect(ipam.HasAllocatedIPs(ctx, public)).To(BeFalse())
	})

	It("should allocate ipv6", func() {
		v6 := &zone.Zone{UUID: "zone-v6", Name: "v6", Pools: []*zone.IpPool{{Name: "default", Subnets: []*zone.Subnet{{Ip: "2001:db8::", PrefixLength: 126}}}}}

		first, err := ipam.AcquireIP(ctx, v6, "default", "vip-1")

		Expect(err).ToNot(HaveOccurred())
		Expect(first.String()).To(Equal("2001:db8::"), "ipv6 has no network nor broadcast address to keep")
	})

	Context("with pools", func() {
		var pooledZone *zone.Zone

		subnetPool := func(name, ip string) *zone.IpPool {
			return &zone.IpPool{Name: name, Subnets: []*zone.Subnet{{Ip: ip, PrefixLength: 30}}}
		}
		withPools := func(pools ...*zone.IpPool) *zone.Zone {
			return &zone.Zone{UUID: "zone-pools", Name: "pools", Pools: pools}
		}

		BeforeEach(func() {
			pooledZone = withPools(subnetPool("a", "10.0.0.0"), subnetPool("b", "10.0.1.0"))
			first, err := ipam.AcquireIP(ctx, pooledZone, "a", "vip-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(first.String()).To(Equal("10.0.0.1"))
		})

		It("should acquire in the asked pool only", func() {
			ip, err := ipam.AcquireIP(ctx, pooledZone, "b", "vip-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(ip.String()).To(Equal("10.0.1.1"))

			_, err = ipam.AcquireIP(ctx, pooledZone, "c", "vip-3")
			Expect(err).To(MatchError(ErrPoolNotFound))
		})

		It("should add a pool while another holds ips", func() {
			grown := withPools(subnetPool("a", "10.0.0.0"), subnetPool("b", "10.0.1.0"), subnetPool("c", "10.0.2.0"))
			Expect(ipam.SyncZone(ctx, grown)).To(Succeed())

			ip, err := ipam.AcquireIP(ctx, grown, "c", "vip-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(ip.String()).To(Equal("10.0.2.1"))
		})

		It("should remove a pool without allocations", func() {
			withoutB := withPools(subnetPool("a", "10.0.0.0"))
			Expect(ipam.SyncZone(ctx, withoutB)).To(Succeed())

			_, err := ipam.AcquireIP(ctx, withoutB, "b", "vip-2")
			Expect(err).To(MatchError(ErrPoolNotFound))
			Expect(ipam.AcquireSpecificIP(ctx, withoutB, netip.MustParseAddr("10.0.1.1"), "vip-2")).To(MatchError(ErrIPOutOfRange))
		})

		It("should refuse to remove a pool holding an ip and change nothing", func() {
			withoutA := withPools(subnetPool("b", "10.0.1.0"), subnetPool("c", "10.0.2.0"))
			Expect(ipam.SyncZone(ctx, withoutA)).To(MatchError(ErrIPsOutOfRanges))

			ip, err := ipam.AcquireIP(ctx, pooledZone, "a", "vip-x")
			Expect(err).ToNot(HaveOccurred())
			Expect(ip.String()).To(Equal("10.0.0.2"))
			Expect(ipam.AcquireSpecificIP(ctx, withoutA, netip.MustParseAddr("10.0.2.1"), "vip-y")).To(MatchError(ErrIPOutOfRange), "pool c was not inserted")
		})

		It("should refuse to shrink a range holding an ip", func() {
			shrunk := withPools(
				&zone.IpPool{Name: "a", IpRanges: []*zone.IpRange{{IpMin: "10.0.0.2", IpMax: "10.0.0.2"}}},
				subnetPool("b", "10.0.1.0"),
			)
			Expect(ipam.SyncZone(ctx, shrunk)).To(MatchError(ErrIPsOutOfRanges))
		})

		It("should keep the allocations of a renamed pool", func() {
			renamed := withPools(subnetPool("a2", "10.0.0.0"), subnetPool("b", "10.0.1.0"))
			Expect(ipam.SyncZone(ctx, renamed)).To(Succeed())

			Expect(ipam.AcquireSpecificIP(ctx, renamed, netip.MustParseAddr("10.0.0.1"), "vip-1")).To(Succeed())
			Expect(ipam.AcquireSpecificIP(ctx, renamed, netip.MustParseAddr("10.0.0.1"), "vip-2")).To(MatchError(ErrIPAllocated))
			next, err := ipam.AcquireIP(ctx, renamed, "a2", "vip-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(next.String()).To(Equal("10.0.0.2"))
		})

		It("should grow a pool holding an ip with a range next to its subnet", func() {
			grownA := subnetPool("a", "10.0.0.0")
			grownA.IpRanges = []*zone.IpRange{{IpMin: "10.0.0.4", IpMax: "10.0.0.6"}}
			grown := withPools(grownA, subnetPool("b", "10.0.1.0"))
			Expect(ipam.SyncZone(ctx, grown)).To(Succeed())

			Expect(ipam.AcquireSpecificIP(ctx, grown, netip.MustParseAddr("10.0.0.5"), "vip-2")).To(Succeed())
		})

		It("should acquire a specific ip whatever its pool", func() {
			Expect(ipam.AcquireSpecificIP(ctx, pooledZone, netip.MustParseAddr("10.0.1.2"), "vip-3")).To(Succeed())
		})

		It("should refuse to remove every pool while an ip is allocated", func() {
			Expect(ipam.SyncZone(ctx, withPools())).To(MatchError(ErrIPsOutOfRanges))
		})

		It("should remove every pool once their ips are released", func() {
			Expect(ipam.ReleaseIP(ctx, pooledZone, netip.MustParseAddr("10.0.0.1"))).To(Succeed())

			Expect(ipam.SyncZone(ctx, withPools())).To(Succeed())
			Expect(ipam.SyncZone(ctx, withPools(subnetPool("b", "10.0.1.0")))).To(Succeed())
			Expect(ipam.AcquireSpecificIP(ctx, pooledZone, netip.MustParseAddr("10.0.0.1"), "vip-1")).To(MatchError(ErrIPOutOfRange))
		})

		It("should refuse a zone with overlapping pools", func() {
			Expect(ipam.SyncZone(ctx, withPools(subnetPool("a", "10.0.0.0"), subnetPool("b", "10.0.0.0")))).To(HaveOccurred())
		})
	})
})
