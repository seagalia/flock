package secret_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
)

var _ = Describe("Secret references", func() {
	DescribeTable("ParseRef",
		func(value string, wantRef Ref, wantIsRef, wantErr bool) {
			ref, isRef, err := ParseRef(value)
			Expect(isRef).To(Equal(wantIsRef))
			Expect(err != nil).To(Equal(wantErr))
			Expect(ref).To(Equal(wantRef))
		},
		Entry("plain value", "payments", Ref{}, false, false),
		Entry("reference", "secret://6f1c/token", Ref{SecretUUID: "6f1c", Key: "token"}, true, false),
		Entry("no key", "secret://6f1c", Ref{}, true, true),
		Entry("empty key", "secret://6f1c/", Ref{}, true, true),
		Entry("empty uuid", "secret:///token", Ref{}, true, true),
		Entry("extra segment", "secret://6f1c/token/x", Ref{}, true, true),
	)
	It("Ref prints as a reference", func() {
		Expect(Ref{SecretUUID: "u1", Key: "k"}.String()).To(Equal("secret://u1/k"))
	})
	It("Blanked keeps a nil data nil", func() {
		Expect((&Secret{Name: "s"}).Blanked().GetData()).To(BeEmpty())
	})
	It("Blanked keeps the keys and empties the values without touching the original", func() {
		s := &Secret{Name: "s", Data: map[string]string{"a": "1", "b": "2"}}
		Expect(s.Blanked().GetData()).To(Equal(map[string]string{"a": "", "b": ""}))
		Expect(s.GetData()["a"]).To(Equal("1"))
	})
	It("Signature ignores the values", func() {
		first := &Secret{Name: "s", Data: map[string]string{"a": "1"}}
		second := &Secret{Name: "s", Data: map[string]string{"a": "other"}}
		firstSignature, err := first.Signature()
		Expect(err).ToNot(HaveOccurred())
		secondSignature, err := second.Signature()
		Expect(err).ToNot(HaveOccurred())
		Expect(firstSignature).To(Equal(secondSignature))
	})
	It("handles its identifier and metadata", func() {
		s := &Secret{}
		s.SetIdentifier("uuid-1")
		Expect(s.GetIdentifier()).To(Equal("uuid-1"))
		s.AddMetadata("k", "v")
		Expect(s.GetMetadata()).To(Equal(map[string]string{"k": "v"}))
		Expect(s.DeleteMetadata("k")).To(BeEmpty())
		s.SetMetadata(map[string]string{"a": "b"})
		Expect(s.GetMetadata()).To(Equal(map[string]string{"a": "b"}))
	})
})
