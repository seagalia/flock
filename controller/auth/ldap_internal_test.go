package auth

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ldapURL", func() {
	It("uses ldaps when ssl is enabled", func() {
		Expect(ldapURL("ldap.example.com:636", true)).To(Equal("ldaps://ldap.example.com:636"))
	})
	It("uses ldap otherwise", func() {
		Expect(ldapURL("ldap.example.com:389", false)).To(Equal("ldap://ldap.example.com:389"))
	})
})
