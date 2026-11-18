package cli

import (
	"encoding/base64"
	"encoding/json"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli/app"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
)

func savedConfig(path string) *app.Config {
	GinkgoHelper()
	content, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	cnf := &app.Config{}
	Expect(json.Unmarshal(content, cnf)).To(Succeed())
	return cnf
}

func encodedShare(share UserShare) string {
	GinkgoHelper()
	content, err := json.Marshal(share)
	Expect(err).NotTo(HaveOccurred())
	return base64.StdEncoding.EncodeToString(content)
}

var _ = Describe("set-as-user", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcloginconnect.LoginMeProcedure, reply(&svclogin.MeResponse{Username: "admin", IsAdmin: true}))
	})

	It("saves the user to act as", func() {
		out, err := runner.run("set-as-user", "--username", "alice", "--group", "network")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("You can now list as user alice with groups network"))
		Expect(savedConfig(runner.configPath).AsUser).To(Equal(&app.AsUser{Username: "alice", Groups: []string{"network"}}))
	})

	It("saves the user from a share code of the same server", func() {
		share := encodedShare(UserShare{Username: "bob", Groups: []string{"dev"}, Host: "flock.local:443"})

		out, err := runner.run("sau", "--from-share", share)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("You can now list as user bob"))
		Expect(savedConfig(runner.configPath).AsUser.Username).To(Equal("bob"))
	})

	It("switches server when the share code comes from another one", func() {
		share := encodedShare(UserShare{Username: "bob", Host: "other.local:443", Http1Transport: true, Http1Scheme: "http"})

		out, err := runner.run("set-as-user", "--from-share", share)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("please now use login"))
		saved := savedConfig(runner.configPath)
		Expect(saved.Host).To(Equal("other.local:443"))
		Expect(saved.Http1Transport).To(BeTrue())
		Expect(saved.Http1Scheme).To(Equal("http"))
	})

	It("clears the user with --clear", func() {
		runner.config.AsUser = &app.AsUser{Username: "alice"}

		out, err := runner.run("set-as-user", "--clear")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("User set cleared."))
		Expect(savedConfig(runner.configPath).AsUser).To(BeNil())
	})

	It("requires a user", func() {
		_, err := runner.run("set-as-user")

		Expect(err).To(MatchError(ContainSubstring("you need to set a username/groups")))
	})

	It("rejects groups without a username and saves nothing", func() {
		_, err := runner.run("set-as-user", "--group", "network")

		Expect(err).To(MatchError("username cannot be empty"))
		Expect(runner.configPath).NotTo(BeAnExistingFile())
	})

	It("rejects a share code without username", func() {
		_, err := runner.run("set-as-user", "--from-share", encodedShare(UserShare{Host: "flock.local:443"}))

		Expect(err).To(MatchError("username cannot be empty"))
	})

	It("is reserved to admins", func() {
		runner.conn.Handle(svcloginconnect.LoginMeProcedure, reply(&svclogin.MeResponse{Username: "alice"}))

		_, err := runner.run("set-as-user", "--username", "bob")

		Expect(err).To(MatchError(ContainSubstring("you need to be admin")))
		Expect(runner.configPath).NotTo(BeAnExistingFile())
	})
})
