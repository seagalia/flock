//go:build e2e

package e2e_test

import (
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/e2e/controller/env"
)

var _ = Describe("Environment", func() {
	It("gives the logs and removes a controller that cannot boot", func(ctx SpecContext) {
		const alias = "controller-cannot-boot"
		_, err := e2eEnv.StartController(ctx, env.ControllerOptions{
			Name:          alias,
			Store:         env.StoreSqlite,
			Instantiators: []map[string]any{{"name": "broken"}},
		})
		Expect(err).To(MatchError(And(
			ContainSubstring("container logs:"),
			ContainSubstring("instantiator config: at least one zone is required"),
		)))

		out, err := exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", "name="+alias).CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))
		Expect(strings.TrimSpace(string(out))).To(BeEmpty(), "containers left for %s", alias)
	}, NodeTimeout(5*time.Minute))
})
