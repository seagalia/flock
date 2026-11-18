package cli

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ai install-skill", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("prints the embedded skill", func() {
		out, err := runner.run("ai", "install-skill", "--stdout")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal(skillContent))
		Expect(out).To(HavePrefix("---\nname: flock\n"))
	})

	It("installs the skill in the claude skills of the user", func() {
		home := GinkgoT().TempDir()
		GinkgoT().Setenv("HOME", home)

		out, err := runner.run("ai", "install-skill")

		Expect(err).NotTo(HaveOccurred())
		skillPath := filepath.Join(home, ".claude", "skills", "flock", "SKILL.md")
		Expect(out).To(ContainSubstring(skillPath))
		Expect(os.ReadFile(skillPath)).To(BeEquivalentTo(skillContent))
	})

	It("installs the skill in the current project with --project", func() {
		project := GinkgoT().TempDir()
		GinkgoT().Chdir(project)

		_, err := runner.run("ai", "install-skill", "--project")

		Expect(err).NotTo(HaveOccurred())
		Expect(filepath.Join(project, ".claude", "skills", "flock", "SKILL.md")).To(BeAnExistingFile())
	})
})
