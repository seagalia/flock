package zone_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("Zone", func() {
	It("should be identified by its uuid", func() {
		aZone := &Zone{UUID: "zone-1", Name: "production"}
		Expect(aZone.GetIdentifier()).To(Equal("zone-1"))
		aZone.SetIdentifier("zone-2")
		Expect(aZone.GetUUID()).To(Equal("zone-2"))
		Expect(aZone.GetName()).To(Equal("production"))
		expectedSig, err := helpers.MessageSignature(aZone)
		Expect(err).ToNot(HaveOccurred())
		Expect(aZone.Signature()).To(Equal(expectedSig))
	})

	It("should tell when it has no ip to allocate", func() {
		Expect((&Zone{}).CouldNotAllocateIP()).To(BeTrue())
		Expect((&Zone{Pools: []*IpPool{{Name: "default"}}}).CouldNotAllocateIP()).To(BeFalse())
	})

	Describe("DefaultPool", func() {
		It("should skip private pools", func() {
			aZone := &Zone{Pools: []*IpPool{
				{Name: "internal", Privacy: IpPool_PRIVATE},
				{Name: "edge", Privacy: IpPool_PUBLIC},
			}}
			Expect(aZone.DefaultPool().GetName()).To(Equal("edge"))
		})

		It("should be nil when all pools are private", func() {
			aZone := &Zone{Pools: []*IpPool{{Name: "internal", Privacy: IpPool_PRIVATE}}}
			Expect(aZone.DefaultPool()).To(BeNil())
		})
	})

	Describe("FindPool", func() {
		aZone := &Zone{Pools: []*IpPool{
			{Name: "a", Metadata: map[string]string{"ns": "y"}},
			{Name: "b", Metadata: map[string]string{"ns": "x"}},
			{Name: "c", Metadata: map[string]string{"ns": "x"}},
		}}

		It("should find by name", func() {
			Expect(aZone.FindPool("b", nil).GetName()).To(Equal("b"))
		})

		It("should find the first pool matching the metadata selector", func() {
			Expect(aZone.FindPool("", map[string]string{"ns": "x"}).GetName()).To(Equal("b"))
		})

		It("should be nil without name nor selector", func() {
			Expect(aZone.FindPool("", nil)).To(BeNil())
		})

		It("should be nil when nothing matches", func() {
			Expect(aZone.FindPool("zz", nil)).To(BeNil())
			Expect(aZone.FindPool("", map[string]string{"ns": "none"})).To(BeNil())
		})
	})

	It("should manage metadata", func() {
		aZone := &Zone{}
		Expect(aZone.DeleteMetadata("key")).To(BeNil())
		aZone.AddMetadata("key", "value")
		Expect(aZone.GetMetadata()).To(HaveKeyWithValue("key", "value"))
		Expect(aZone.DeleteMetadata("key")).To(BeEmpty())
		aZone.SetMetadata(map[string]string{"other": "value"})
		Expect(aZone.GetMetadata()).To(HaveKey("other"))
	})
})
