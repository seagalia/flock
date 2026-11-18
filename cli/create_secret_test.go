package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
)

var _ = Describe("create-secret", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcsecretconnect.SecretCreateSecretProcedure, reply(&svcsecret.SecretResponse{
			Secret: &secret.Secret{UUID: resourceUuid, Name: "s", Data: map[string]string{"a": "1"}},
		}))
	})

	It("sends the name and the data", func() {
		out, err := runner.run("create-secret", "--name", "s", "--data", "a=1")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Secret created successfully"))
		sent := lastRequest[*svcsecret.CreateSecretRequest](runner.conn, svcsecretconnect.SecretCreateSecretProcedure).GetSecret()
		Expect(sent.GetName()).To(Equal("s"))
		Expect(sent.GetData()).To(Equal(map[string]string{"a": "1"}))
	})

	It("reads a value from a file", func() {
		_, err := runner.run("create-secret", "--name", "s", "--from-file", "k="+writeTempFile("k.txt", "content"))

		Expect(err).NotTo(HaveOccurred())
		sent := lastRequest[*svcsecret.CreateSecretRequest](runner.conn, svcsecretconnect.SecretCreateSecretProcedure).GetSecret()
		Expect(sent.GetData()).To(Equal(map[string]string{"k": "content"}))
	})

	It("prints no value with --json", func() {
		out, err := runner.run("create-secret", "--name", "s", "--data", "a=1", "--json")

		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).To(HaveKeyWithValue("data", HaveKeyWithValue("a", "")))
	})
})
