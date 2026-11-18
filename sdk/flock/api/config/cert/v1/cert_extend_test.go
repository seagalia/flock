package cert_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("Certificate", func() {
	It("should be identified by its uuid", func() {
		certificate := &Certificate{UUID: "cert-1"}
		Expect(certificate.GetIdentifier()).To(Equal("cert-1"))
		certificate.SetIdentifier("cert-2")
		Expect(certificate.GetUUID()).To(Equal("cert-2"))
		expectedSig, err := helpers.MessageSignature(certificate)
		Expect(err).ToNot(HaveOccurred())
		Expect(certificate.Signature()).To(Equal(expectedSig))
	})

	It("should manage metadata", func() {
		certificate := &Certificate{}
		Expect(certificate.DeleteMetadata("key")).To(BeNil())
		certificate.AddMetadata("key", "value")
		Expect(certificate.GetMetadata()).To(HaveKeyWithValue("key", "value"))
		Expect(certificate.DeleteMetadata("key")).To(BeEmpty())
		certificate.SetMetadata(map[string]string{"other": "value"})
		Expect(certificate.GetMetadata()).To(HaveKey("other"))
	})
})
