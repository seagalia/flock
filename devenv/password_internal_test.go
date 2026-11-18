package devenv

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("resolveAdminPassword", func() {
	var dir, file string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		file = filepath.Join(dir, adminPasswordFile)
	})

	It("should use the env value whatever the mode", func() {
		for _, exposed := range []bool{true, false} {
			password, err := resolveAdminPassword(dir, exposed, "from-env")
			Expect(err).NotTo(HaveOccurred())
			Expect(password).To(Equal("from-env"))
		}
		Expect(file).NotTo(BeAnExistingFile())
	})

	It("should keep admin on loopback", func() {
		password, err := resolveAdminPassword(dir, false, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(password).To(Equal("admin"))
		Expect(file).NotTo(BeAnExistingFile())
	})

	It("should generate, store and reuse a password when exposed", func() {
		first, err := resolveAdminPassword(dir, true, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(first).To(HaveLen(24))

		info, err := os.Stat(file)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))

		second, err := resolveAdminPassword(dir, true, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
	})

	It("should never generate a password starting with a dash, the cli would read it as a flag", func() {
		for range 500 {
			password, err := generatePassword()
			Expect(err).NotTo(HaveOccurred())
			Expect(password).NotTo(HavePrefix("-"))
		}
	})

	It("should prefer the env over a stored password", func() {
		Expect(os.WriteFile(file, []byte("stored"), 0o600)).To(Succeed())

		password, err := resolveAdminPassword(dir, true, "from-env")
		Expect(err).NotTo(HaveOccurred())
		Expect(password).To(Equal("from-env"))

		content, err := os.ReadFile(file)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal("stored"))
	})
})
