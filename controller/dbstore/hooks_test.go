package dbstore_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/controller/dbstore"
)

var _ = Describe("EncryptTransHook", func() {
	newHook := func(secret string) *EncryptTransHook {
		hook, err := NewEncryptTransHook(secret)
		Expect(err).ToNot(HaveOccurred())
		return hook
	}

	It("should revert what it converted", func() {
		hook := newHook("secret")

		encrypted, err := hook.Convert([]byte("content"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(encrypted)).ToNot(ContainSubstring("content"))

		decrypted, err := hook.Revert(encrypted)
		Expect(err).ToNot(HaveOccurred())
		Expect(decrypted).To(Equal([]byte("content")))
	})
	It("should use a new nonce each time the same content is converted", func() {
		hook := newHook("secret")

		first, err := hook.Convert([]byte("content"))
		Expect(err).ToNot(HaveOccurred())
		second, err := hook.Convert([]byte("content"))
		Expect(err).ToNot(HaveOccurred())

		Expect(first[:12]).ToNot(Equal(second[:12]))
		Expect(first).ToNot(Equal(second))
	})
	It("should not revert content encrypted with another secret", func() {
		encrypted, err := newHook("secret").Convert([]byte("content"))
		Expect(err).ToNot(HaveOccurred())

		_, err = newHook("other").Revert(encrypted)
		Expect(err).To(HaveOccurred())
	})
	It("should not revert tampered content", func() {
		hook := newHook("secret")
		encrypted, err := hook.Convert([]byte("content"))
		Expect(err).ToNot(HaveOccurred())

		encrypted[len(encrypted)-1] ^= 0xff
		_, err = hook.Revert(encrypted)
		Expect(err).To(HaveOccurred())
	})
	It("should refuse content shorter than a nonce", func() {
		_, err := newHook("secret").Revert([]byte("short"))
		Expect(err).To(MatchError(ContainSubstring("too short")))
	})
})
