package config_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v2"

	. "github.com/seagalia/flock/sdk/config"
)

const patchEnv = "FLOCK_TEST_CONFIG"

var _ = Describe("ReadFile", func() {
	writeFile := func(content string) string {
		GinkgoHelper()
		path := filepath.Join(GinkgoT().TempDir(), "config.yml")
		Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())
		return path
	}
	readMerged := func(content, patch string) map[string]any {
		GinkgoHelper()
		GinkgoT().Setenv(patchEnv, patch)
		merged, err := ReadFile(writeFile(content), patchEnv)
		Expect(err).NotTo(HaveOccurred())
		mapping := map[string]any{}
		Expect(yaml.Unmarshal(merged, &mapping)).To(Succeed())
		return mapping
	}

	It("gives the file as it is when the env var is not set", func() {
		content := "# kept\nserver:\n  port: 80\n"
		GinkgoT().Setenv(patchEnv, " ")

		merged, err := ReadFile(writeFile(content), patchEnv)

		Expect(err).NotTo(HaveOccurred())
		Expect(string(merged)).To(Equal(content))
	})

	It("merges the maps key by key and adds the keys the file lacks", func() {
		merged := readMerged("server:\n  host: a\n  port: 80\n", "server:\n  port: 81\ndocker:\n  enabled: true\n")

		Expect(merged).To(HaveKeyWithValue("server", map[any]any{"host": "a", "port": 81}))
		Expect(merged).To(HaveKeyWithValue("docker", map[any]any{"enabled": true}))
	})

	It("replaces a list, a scalar, and clears a key with null", func() {
		merged := readMerged(
			"zones: [a, b]\nlevel: info\ntls:\n  ca: x\n",
			"zones: [c]\nlevel: debug\ntls: null\n",
		)

		Expect(merged).To(HaveKeyWithValue("zones", []any{"c"}))
		Expect(merged).To(HaveKeyWithValue("level", "debug"))
		Expect(merged).To(HaveKeyWithValue("tls", BeNil()))
	})

	It("takes the env var whole over an empty file", func() {
		merged := readMerged("", `{"docker": {"enabled": true}}`)

		Expect(merged).To(HaveKeyWithValue("docker", map[any]any{"enabled": true}))
	})

	It("refuses an env var that is not a yaml mapping, naming it", func() {
		GinkgoT().Setenv(patchEnv, "- a\n- b\n")

		_, err := ReadFile(writeFile("level: info\n"), patchEnv)

		Expect(err).To(MatchError(ContainSubstring(patchEnv)))
	})

	It("fails on a missing file", func() {
		_, err := ReadFile(filepath.Join(GinkgoT().TempDir(), "missing.yml"), patchEnv)

		Expect(err).To(MatchError(os.ErrNotExist))
	})
})
