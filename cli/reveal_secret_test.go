package cli

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
)

var _ = Describe("reveal-secret", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcsecretconnect.SecretReadSecretProcedure, reply(&svcsecret.SecretResponse{
			Secret: &secret.Secret{UUID: resourceUuid, Name: "db", Data: map[string]string{"a": "", "b": ""}},
		}))
		runner.conn.Handle(svcsecretconnect.SecretRevealSecretProcedure, reply(&svcsecret.SecretResponse{
			Secret: &secret.Secret{UUID: resourceUuid, Name: "db", Data: map[string]string{"b": "2", "a": "1"}},
		}))
	})

	It("prints key=value lines sorted by key", func() {
		out, err := runner.run("reveal-secret", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("a=1\nb=2\n"))
	})

	It("prints the raw value and a newline with --key", func() {
		out, err := runner.run("reveal-secret", "--key", "a", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("1\n"))
	})

	It("prints only the given key with --key and --json", func() {
		out, err := runner.run("reveal-secret", "--key", "a", "--json", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		// protojson spaces its output at random from one build to the next, so the output is read back
		var revealed struct{ Data map[string]string }
		Expect(json.Unmarshal([]byte(out), &revealed)).To(Succeed())
		Expect(revealed.Data).To(Equal(map[string]string{"a": "1"}))
	})

	It("fails on an unknown key without echoing other values", func() {
		out, err := runner.run("reveal-secret", "--key", "zz", resourceUuid)

		Expect(err).To(MatchError(ContainSubstring("has no key zz")))
		Expect(out).NotTo(ContainSubstring("1"))
	})
})
