package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
)

var _ = Describe("secret", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcsecretconnect.SecretReadSecretProcedure, reply(&svcsecret.SecretResponse{
			Secret: &secret.Secret{
				UUID: resourceUuid, Name: "db",
				Data:     map[string]string{"password": "leaked"},
				Metadata: map[string]string{"env": "prod"},
			},
		}))
	})

	It("prints the keys and the metadata, never a value", func() {
		out, err := runner.run("secret", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Secret " + resourceUuid + " configuration"))
		Expect(out).To(ContainSubstring("password"))
		Expect(out).To(ContainSubstring("env: prod"))
		Expect(out).NotTo(ContainSubstring("leaked"))
	})

	It("prints only the uuid with --uuid", func() {
		out, err := runner.run("secret", "--uuid", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(resourceUuid + "\n"))
	})

	It("prints json with --json", func() {
		out, err := runner.run("secret", "--json", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).To(HaveKeyWithValue("name", "db"))
		Expect(out).NotTo(ContainSubstring("leaked"))
	})
})
