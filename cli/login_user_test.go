package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli/internal/fakeoidc"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Missing credentials are prompted, so only credentials given as flags are tested.
var _ = Describe("login", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcloginconnect.LoginGetServiceAuthProcedure, reply(&svclogin.ServiceAuth{Type: "LDAP"}))
		runner.conn.Handle(svcloginconnect.LoginGetTokenProcedure, reply(&svclogin.GetTokenResponse{
			Token:     "access",
			ExpiresAt: timestamppb.New(time.Now().Add(time.Hour)),
		}))
	})

	It("logs in with username and password and saves the config without the password", func() {
		out, err := runner.run("login", "--host", "flock.local", "--username", "alice", "--password", "secret")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Login successful."))
		sent := lastRequest[*svclogin.GetTokenRequest](runner.conn, svcloginconnect.LoginGetTokenProcedure)
		Expect(sent.GetUsername()).To(Equal("alice"))
		Expect(sent.GetPassword()).To(Equal("secret"))

		saved := savedConfig(runner.configPath)
		Expect(saved.Host).To(Equal("flock.local:443"), "the default port is added")
		Expect(saved.Username).To(Equal("alice"))
		Expect(os.ReadFile(runner.configPath)).NotTo(ContainSubstring("secret"))
	})

	It("stores the flock token in the user's cache dir", func() {
		_, err := runner.run("login", "--host", "flock.local:443", "--username", "alice", "--password", "secret")

		Expect(err).NotTo(HaveOccurred())
		tokenFiles, err := filepath.Glob(filepath.Join(sessionsDir(), "flock.flock_local_443.*.token"))
		Expect(err).NotTo(HaveOccurred())
		Expect(tokenFiles).To(HaveLen(1))
		raw, err := os.ReadFile(tokenFiles[0])
		Expect(err).NotTo(HaveOccurred())
		stored := map[string]any{}
		Expect(json.Unmarshal(raw, &stored)).To(Succeed())
		Expect(stored).To(HaveKeyWithValue("kind", "flock"))
		Expect(stored).To(HaveKeyWithValue("access_token", "access"))
	})

	Describe("on an OIDC controller", func() {
		BeforeEach(func() {
			provider, err := fakeoidc.New()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(provider.Close)
			runner.conn.Handle(svcloginconnect.LoginGetServiceAuthProcedure, reply(&svclogin.ServiceAuth{
				Type: "OIDC",
				Oidc: &svclogin.OidcProvider{IssuerUrl: provider.Issuer(), ClientId: fakeoidc.ClientId, Scopes: []string{"openid"}},
			}))
		})

		It("logs a machine in with client credentials and never asks the controller for a token", func() {
			out, err := runner.run("login", "--host", "flock.local:443",
				"--client_id", fakeoidc.ClientId, "--client_secret", fakeoidc.ClientSecret)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("Login successful."))
			Expect(runner.conn.Requests(svcloginconnect.LoginGetTokenProcedure)).To(BeEmpty())
			tokenFiles, err := filepath.Glob(filepath.Join(sessionsDir(), "flock.flock_local_443.*.token"))
			Expect(err).NotTo(HaveOccurred())
			Expect(tokenFiles).To(HaveLen(1))
			raw, err := os.ReadFile(tokenFiles[0])
			Expect(err).NotTo(HaveOccurred())
			Expect(raw).To(ContainSubstring(`"kind":"oidc_client"`))
			Expect(raw).NotTo(ContainSubstring(fakeoidc.ClientSecret))
			Expect(savedConfig(runner.configPath).Host).To(Equal("flock.local:443"))
			Expect(os.ReadFile(runner.configPath)).NotTo(ContainSubstring(fakeoidc.ClientSecret))
		})

		It("warns that a username and a password are ignored", func() {
			out, err := runner.run("login", "--host", "flock.local:443", "--username", "alice", "--password", "secret",
				"--client_id", fakeoidc.ClientId, "--client_secret", fakeoidc.ClientSecret)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("The controller logs in with OIDC, --username and --password are ignored"))
			Expect(runner.conn.Requests(svcloginconnect.LoginGetTokenProcedure)).To(BeEmpty())
		})

		It("refuses a client id without secret", func() {
			_, err := runner.run("login", "--host", "flock.local:443", "--client_id", fakeoidc.ClientId)

			Expect(err).To(MatchError("the client credentials login needs a client secret too"))
			Expect(runner.configPath).NotTo(BeAnExistingFile())
		})
	})

	It("does not save the config when the server rejects the credentials", func() {
		runner.conn.Handle(svcloginconnect.LoginGetTokenProcedure, failWith(os.ErrPermission))

		_, err := runner.run("login", "--host", "flock.local:443", "--username", "alice", "--password", "wrong")

		Expect(err).To(MatchError(ContainSubstring("Error when checking credentials")))
		Expect(runner.configPath).NotTo(BeAnExistingFile())
	})
})

func sessionsDir() string {
	GinkgoHelper()
	cacheDir, err := os.UserCacheDir()
	Expect(err).NotTo(HaveOccurred())
	return filepath.Join(cacheDir, "flock", "sessions")
}
