package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli/updater"
)

// Only the paths that do not replace the binary: an update would overwrite the test binary.
var _ = Describe("self-update", func() {
	var (
		runner   *commandRunner
		requests atomic.Int32
		status   int
		tag      string
	)

	BeforeEach(func() {
		runner = newCommandRunner()
		status, tag = http.StatusOK, "v1.2.0"
		requests.Store(0)
		previous := binInfoGlobal
		DeferCleanup(func() { binInfoGlobal = previous })

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.WriteHeader(status)
			fmt.Fprintf(w, `{"tag_name": %q}`, tag)
		}))
		restore := updater.SetBaseURLs(server.URL, server.URL)
		DeferCleanup(func() {
			restore()
			server.Close()
		})
	})

	It("says when the binary is up to date", func() {
		binInfoGlobal = updater.BinInfo{Version: "1.2.0"}

		out, err := runner.run("self-update")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("You are up to date."))
	})

	It("only warns when the latest version cannot be read", func() {
		binInfoGlobal = updater.BinInfo{Version: "1.2.0"}
		tag = "not-a-version"

		out, err := runner.run("self-update")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("When getting version"))
	})

	Describe("the update check before a command", func() {
		It("stays quiet when GitHub fails and the command still runs", func() {
			binInfoGlobal = updater.BinInfo{Version: "1.0.0"}
			status = http.StatusForbidden

			out, err := runner.run("clear-session")

			Expect(err).NotTo(HaveOccurred())
			Expect(out).NotTo(ContainSubstring("version"))
			Expect(out).NotTo(ContainSubstring("403"))
			Expect(requests.Load()).To(BeEquivalentTo(1))

			out, err = runner.run("clear-session")

			Expect(err).NotTo(HaveOccurred())
			Expect(out).NotTo(ContainSubstring("version"))
			Expect(requests.Load()).To(BeEquivalentTo(1))
		})

		It("warns of a new version once a day only", func() {
			binInfoGlobal = updater.BinInfo{Version: "1.0.0"}

			first, err := runner.run("clear-session")
			Expect(err).NotTo(HaveOccurred())
			second, err := runner.run("clear-session")
			Expect(err).NotTo(HaveOccurred())

			Expect(first).To(ContainSubstring("New version 1.2.0 is available"))
			Expect(second).To(ContainSubstring("New version 1.2.0 is available"))
			Expect(requests.Load()).To(BeEquivalentTo(1))
			Expect(filepath.Join(filepath.Dir(runner.configPath), "update-check.json")).To(BeAnExistingFile())
		})
	})
})
