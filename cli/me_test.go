package cli

import (
	"encoding/base64"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
	"golang.org/x/oauth2"
)

var _ = Describe("me", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcloginconnect.LoginMeProcedure, reply(&svclogin.MeResponse{
			Username: "alice",
			Groups:   []string{"network", "admins"},
			IsAdmin:  true,
		}))
	})

	It("shows the user, groups and admin flag", func() {
		out, err := runner.run("me")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("alice"))
		Expect(out).To(ContainSubstring("network, admins"))
		Expect(out).To(MatchRegexp(`Are You admin \?:\s+Yes`))
		Expect(out).To(MatchRegexp(`Token:\s+session-token`))
	})

	It("prints the user as json with --json", func() {
		out, err := runner.run("me", "--json")

		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).To(And(
			HaveKeyWithValue("username", "alice"),
			HaveKeyWithValue("is_admin", true),
			HaveKeyWithValue("token", "session-token"),
			HaveKeyWithValue("expires_at", sessionExpiry.In(time.Local).Format(time.RFC3339)),
		))
	})

	It("shows an unknown expiry for a bearer given with FLOCK_TOKEN", func() {
		runner.tokenSource = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "env-token"})

		out, err := runner.run("me")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(MatchRegexp(`Token:\s+env-token`))
		Expect(out).To(MatchRegexp(`Expires At:\s+unknown`))

		out, err = runner.run("me", "--json")
		Expect(err).NotTo(HaveOccurred())
		Expect(parseJSON(out)).NotTo(HaveKey("expires_at"))
	})

	It("gives a share code with the user and the endpoint with --share", func() {
		runner.config.Http1Transport = true

		out, err := runner.run("me", "--share")

		Expect(err).NotTo(HaveOccurred())
		code := strings.TrimSpace(out[strings.LastIndex(out, " "):])
		decoded, err := base64.StdEncoding.DecodeString(code)
		Expect(err).NotTo(HaveOccurred())
		Expect(decoded).To(MatchJSON(`{
			"username": "alice", "groups": ["network", "admins"],
			"host": "flock.local:443", "http1_transport": true, "http1_scheme": ""
		}`))
	})
})
