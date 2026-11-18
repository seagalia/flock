package bigip

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("parseMemberStates", func() {
	It("reads the documented answer, route domain and IPv6 member names included", func() {
		body, err := os.ReadFile("bigipmock/testdata/pool_members.json")
		Expect(err).ToNot(HaveOccurred())

		Expect(parseMemberStates(body)).To(Equal(map[string]bool{
			"10.0.0.10:8080":     true,
			"[2001:db8::1]:8080": false,
		}))
	})

	It("keeps the members a FQDN member populated and leaves out the FQDN member itself", func() {
		body := []byte(`{"items":[
			{"name":"www.example.com:8080","address":"any6","ephemeral":"false","state":"fqdn-up"},
			{"name":"_auto_10.0.0.20:8080","address":"10.0.0.20","ephemeral":"true","state":"up"}
		]}`)

		Expect(parseMemberStates(body)).To(Equal(map[string]bool{"10.0.0.20:8080": true}))
	})
})
