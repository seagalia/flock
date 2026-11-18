package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("delete-secret", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcsecretconnect.SecretReadSecretProcedure, reply(&svcsecret.SecretResponse{
			Secret: &secret.Secret{UUID: resourceUuid, Name: "s"},
		}))
		runner.conn.Handle(svcsecretconnect.SecretDeleteSecretProcedure, reply(&emptypb.Empty{}))
	})

	It("deletes the secret", func() {
		out, err := runner.run("delete-secret", "--force", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Secret successfully deleted"))
		Expect(runner.conn.Requests(svcsecretconnect.SecretDeleteSecretProcedure)).To(HaveLen(1))
	})

	It("refuses a secret managed by a provisioner", func() {
		runner.conn.Handle(svcsecretconnect.SecretReadSecretProcedure, reply(&svcsecret.SecretResponse{
			Secret: &secret.Secret{UUID: resourceUuid, Name: "s", Metadata: map[string]string{"flock.autoprovision": "true"}},
		}))

		_, err := runner.run("delete-secret", "--force", resourceUuid)

		Expect(err).To(HaveOccurred())
		Expect(runner.conn.Requests(svcsecretconnect.SecretDeleteSecretProcedure)).To(BeEmpty())
	})
})
