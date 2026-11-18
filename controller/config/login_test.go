package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/controller/config"
	"go.yaml.in/yaml/v2"
)

var _ = Describe("Login", func() {
	Context("LdapLogin", func() {
		It("should set defaults", func() {
			ldap := &LdapLogin{}
			err := yaml.Unmarshal([]byte("bind_dn: cn=admin\nbind_password: pass\naddress: ldap:389\n"), ldap)
			Expect(err).ToNot(HaveOccurred())

			Expect(ldap.SearchBaseDns).To(Equal("dc=com"))
			Expect(ldap.SearchFilter).To(Equal("(objectClass=organizationalPerson)&(uid=%s)"))
			Expect(ldap.GroupAttribute).To(Equal("cn"))
		})
		DescribeTable("should reject ldap missing field",
			func(content string, errMsg string) {
				err := yaml.Unmarshal([]byte(content), &LdapLogin{})
				Expect(err).To(MatchError(ContainSubstring(errMsg)))
			},
			Entry("no bind dn", "bind_password: pass\naddress: ldap:389\n", "bind_dn is required"),
			Entry("no bind password", "bind_dn: cn=admin\naddress: ldap:389\n", "bind_password is required"),
			Entry("no address", "bind_dn: cn=admin\nbind_password: pass\n", "address is required"),
		)
	})

	Context("BasicAuthLogin", func() {
		It("should load a user", func() {
			basic := &BasicAuthLogin{}
			err := yaml.Unmarshal([]byte("username: user\npassword: pass\ngroups: [g1]\n"), basic)
			Expect(err).ToNot(HaveOccurred())
			Expect(basic.Groups).To(Equal([]string{"g1"}))
		})
		It("should require username", func() {
			err := yaml.Unmarshal([]byte("password: pass\n"), &BasicAuthLogin{})
			Expect(err).To(MatchError(ContainSubstring("user is required")))
		})
		It("should require password", func() {
			err := yaml.Unmarshal([]byte("username: user\n"), &BasicAuthLogin{})
			Expect(err).To(MatchError(ContainSubstring("password is required")))
		})
	})
})
