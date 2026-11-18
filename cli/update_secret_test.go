package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
)

var _ = Describe("update-secret", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcsecretconnect.SecretReadSecretProcedure, reply(&svcsecret.SecretResponse{
			Secret: &secret.Secret{UUID: resourceUuid, Name: "s", Data: map[string]string{"a": "", "c": ""}, Metadata: map[string]string{"env": "prod"}},
		}))
		runner.conn.Handle(svcsecretconnect.SecretUpdateSecretProcedure, reply(&svcsecret.SecretResponse{}))
	})

	It("keeps the stored keys with empty values, adds a key and drops a removed one", func() {
		out, err := runner.run("update-secret", "--force", "--data", "b=hunter2", "--remove-key", "a", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Secret updated successfully"))
		Expect(out).NotTo(ContainSubstring("hunter2"))
		sent := lastRequest[*svcsecret.UpdateSecretRequest](runner.conn, svcsecretconnect.SecretUpdateSecretProcedure)
		Expect(sent.GetUUID()).To(Equal(resourceUuid))
		Expect(sent.GetSecret().GetName()).To(Equal("s"))
		Expect(sent.GetSecret().GetData()).To(Equal(map[string]string{"b": "hunter2", "c": ""}))
		Expect(sent.GetSecret().GetMetadata()).To(Equal(map[string]string{"env": "prod"}))
	})

	It("changes the value of an existing key", func() {
		_, err := runner.run("update-secret", "--force", "--data", "a=new", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		sent := lastRequest[*svcsecret.UpdateSecretRequest](runner.conn, svcsecretconnect.SecretUpdateSecretProcedure)
		Expect(sent.GetSecret().GetData()).To(Equal(map[string]string{"a": "new", "c": ""}))
	})

	It("refuses a value read from stdin without --force, before reading it", func() {
		_, err := runner.run("update-secret", "--data", "a=-", resourceUuid)

		Expect(err).To(MatchError("a value read from stdin needs --force"))
		Expect(runner.conn.Requests(svcsecretconnect.SecretUpdateSecretProcedure)).To(BeEmpty())
	})

	It("refuses to remove a key the secret does not have", func() {
		_, err := runner.run("update-secret", "--force", "--remove-key", "zz", resourceUuid)

		Expect(err).To(MatchError(ContainSubstring("has no key zz")))
		Expect(runner.conn.Requests(svcsecretconnect.SecretUpdateSecretProcedure)).To(BeEmpty())
	})
})
