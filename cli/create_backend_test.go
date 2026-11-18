package cli

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
)

var _ = Describe("create-backend", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("sends the backend from the file without its uuid", func() {
		runner.conn.Handle(svcbackendconnect.BackendCreateBackendProcedure, reply(&svcbackend.BackendResponse{}))

		out, err := runner.run("create-backend", "--force", "--file", writeTempFile("backend.yml", "name: backend\nUUID: "+resourceUuid))

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Backend created successfully"))
		sent := lastRequest[*svcbackend.CreateBackendRequest](runner.conn, svcbackendconnect.BackendCreateBackendProcedure).GetBackend()
		Expect(sent.GetName()).To(Equal("backend"))
		Expect(sent.GetUUID()).To(BeEmpty())
	})

	It("sends a secret reference by name as its uuid form", func() {
		runner.conn.Handle(svcsecretconnect.SecretListSecretProcedure, firstPage(&svcsecret.ListSecretResponse{
			Secrets: []*secret.Secret{{UUID: "sec-1", Name: "s"}},
		}))
		runner.conn.Handle(svcbackendconnect.BackendCreateBackendProcedure, reply(&svcbackend.BackendResponse{}))
		file := writeTempFile("backend.yml", "name: backend\nmetadata:\n  tok: secret://name:s/token")

		_, err := runner.run("create-backend", "--force", "--file", file)

		Expect(err).NotTo(HaveOccurred())
		sent := lastRequest[*svcbackend.CreateBackendRequest](runner.conn, svcbackendconnect.BackendCreateBackendProcedure).GetBackend()
		Expect(sent.GetMetadata()).To(Equal(map[string]string{"tok": "secret://sec-1/token"}))
	})

	It("writes a skeleton file with --init without calling the server", func() {
		path := filepath.Join(GinkgoT().TempDir(), "backend.yml")

		out, err := runner.run("create-backend", "--init", "--file", path)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Initialized json file"))
		Expect(path).To(BeAnExistingFile())
		Expect(runner.conn.Calls()).To(BeEmpty())
	})

	It("fails on an empty file", func() {
		_, err := runner.run("create-backend", "--force", "--file", writeTempFile("backend.yml", ""))

		Expect(err).To(MatchError(ContainSubstring("is empty")))
		Expect(runner.conn.Calls()).To(BeEmpty())
	})
})
