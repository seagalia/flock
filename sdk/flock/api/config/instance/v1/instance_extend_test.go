package instance_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("Instance", func() {
	It("should be identified by its uuid", func() {
		anInstance := &Instance{UUID: "instance-1"}
		Expect(anInstance.GetIdentifier()).To(Equal("instance-1"))
		anInstance.SetIdentifier("instance-2")
		Expect(anInstance.GetUUID()).To(Equal("instance-2"))
		expectedSig, err := helpers.MessageSignature(anInstance)
		Expect(err).ToNot(HaveOccurred())
		Expect(anInstance.Signature()).To(Equal(expectedSig))
	})

	It("should reference its vips and its zone as foreign resources", func() {
		anInstance := &Instance{VipsUuid: []string{"vip-1"}, ZoneUuid: "zone-1"}
		Expect(anInstance.ForeignResources()).To(Equal(map[string][]string{
			helpers.TypeUrl[*vip.VIP]():   {"vip-1"},
			helpers.TypeUrl[*zone.Zone](): {"zone-1"},
		}))
	})

	It("should manage metadata", func() {
		anInstance := &Instance{}
		Expect(anInstance.DeleteMetadata("key")).To(BeNil())
		anInstance.AddMetadata("key", "value")
		Expect(anInstance.GetMetadata()).To(HaveKeyWithValue("key", "value"))
		Expect(anInstance.DeleteMetadata("key")).To(BeEmpty())
		anInstance.SetMetadata(map[string]string{"other": "value"})
		Expect(anInstance.GetMetadata()).To(HaveKey("other"))
	})
})
