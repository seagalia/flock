package config_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/agent/f5/config"
)

const baseConfig = `
controller:
  addr: controller.example:443
  instance_uuid: inst-1
tls:
  cert_file: /etc/flock/agent.crt
  key_file: /etc/flock/agent.key
server:
  listen_host: 127.0.0.1
`

func writeConfig(content string) string {
	path := filepath.Join(GinkgoT().TempDir(), "config.yml")
	Expect(os.WriteFile(path, []byte(content), 0600)).To(Succeed())
	return path
}

func writePasswordFile(content string) string {
	path := filepath.Join(GinkgoT().TempDir(), "password")
	Expect(os.WriteFile(path, []byte(content), 0600)).To(Succeed())
	return path
}

var _ = Describe("Config", func() {
	BeforeEach(func() {
		GinkgoT().Setenv("FLOCK_BIGIP_PASSWORD", "")
	})

	It("applies the defaults", func() {
		cnf, err := ReadConfig(writeConfig(baseConfig + `
bigip:
  urls: [https://bigip-a.example]
  username: flock
  password_file: ` + writePasswordFile("secret\n")))

		Expect(err).ToNot(HaveOccurred())
		bigip := cnf.Bigip
		Expect(bigip.Password).To(Equal("secret"))
		Expect(bigip.LoginProvider).To(Equal("tmos"))
		Expect(bigip.DefaultPartition).To(Equal("flock"))
		Expect(bigip.PartitionMetadataKey).To(Equal("agent.f5.partition"))
		Expect(time.Duration(bigip.Debounce)).To(Equal(time.Second))
		Expect(time.Duration(bigip.MaxDebounce)).To(Equal(10 * time.Second))
		Expect(time.Duration(bigip.MaxIncomplete)).To(Equal(time.Minute))
		Expect(time.Duration(bigip.ResyncInterval)).To(Equal(5 * time.Minute))
		Expect(time.Duration(bigip.GarbageAfterStart)).To(Equal(10 * time.Minute))
		Expect(time.Duration(bigip.RequestTimeout)).To(Equal(2 * time.Minute))
		Expect(cnf.ServerConfig.ListenPort).To(BeEquivalentTo(18000))
	})

	It("takes the password from the environment when there is no password file", func() {
		GinkgoT().Setenv("FLOCK_BIGIP_PASSWORD", "from-env")

		cnf, err := ReadConfig(writeConfig(baseConfig + `
bigip:
  urls: [https://bigip-a.example]
  username: flock
`))

		Expect(err).ToNot(HaveOccurred())
		Expect(cnf.Bigip.Password).To(Equal("from-env"))
	})

	It("prefers the password file over the environment", func() {
		GinkgoT().Setenv("FLOCK_BIGIP_PASSWORD", "from-env")

		cnf, err := ReadConfig(writeConfig(baseConfig + `
bigip:
  urls: [https://bigip-a.example]
  username: flock
  password_file: ` + writePasswordFile("from-file")))

		Expect(err).ToNot(HaveOccurred())
		Expect(cnf.Bigip.Password).To(Equal("from-file"))
	})

	It("requires a password", func() {
		_, err := ReadConfig(writeConfig(baseConfig + `
bigip:
  urls: [https://bigip-a.example]
  username: flock
`))

		Expect(err).To(MatchError(ContainSubstring("bigip password_file or FLOCK_BIGIP_PASSWORD is required")))
	})

	It("requires the bigip block", func() {
		_, err := ReadConfig(writeConfig(baseConfig))

		Expect(err).To(MatchError(ContainSubstring("bigip config is required")))
	})

	It("requires https urls", func() {
		GinkgoT().Setenv("FLOCK_BIGIP_PASSWORD", "x")

		_, err := ReadConfig(writeConfig(baseConfig + `
bigip:
  urls: [http://bigip-a.example]
  username: flock
`))

		Expect(err).To(MatchError(ContainSubstring("must be an https url")))
	})

	It("requires at least one url and a username", func() {
		GinkgoT().Setenv("FLOCK_BIGIP_PASSWORD", "x")

		_, err := ReadConfig(writeConfig(baseConfig + `
bigip:
  username: flock
`))
		Expect(err).To(MatchError(ContainSubstring("bigip urls is required")))

		_, err = ReadConfig(writeConfig(baseConfig + `
bigip:
  urls: [https://bigip-a.example]
`))
		Expect(err).To(MatchError(ContainSubstring("bigip username is required")))
	})

	It("refuses Common as default partition", func() {
		GinkgoT().Setenv("FLOCK_BIGIP_PASSWORD", "x")

		_, err := ReadConfig(writeConfig(baseConfig + `
bigip:
  urls: [https://bigip-a.example]
  username: flock
  default_partition: Common
`))

		Expect(err).To(MatchError(ContainSubstring("Common")))
	})

	It("refuses unknown keys in strict mode only", func() {
		GinkgoT().Setenv("FLOCK_BIGIP_PASSWORD", "x")
		path := writeConfig(baseConfig + `
bigip:
  urls: [https://bigip-a.example]
  username: flock
  debounse: 2s
`)

		_, err := ReadConfigStrict(path)
		Expect(err).To(MatchError(ContainSubstring("debounse")))
		_, err = ReadConfig(path)
		Expect(err).ToNot(HaveOccurred())
	})

	It("loads the sample config", func() {
		GinkgoT().Setenv("FLOCK_BIGIP_PASSWORD", "x")

		_, err := ReadConfigStrict("../config.sample.yml")

		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("ValidTenantName", func() {
	It("accepts AS3 tenant names", func() {
		Expect(ValidTenantName("flock")).To(Succeed())
		Expect(ValidTenantName("team_1.prod-a")).To(Succeed())
	})

	It("refuses invalid or reserved names", func() {
		Expect(ValidTenantName("")).ToNot(Succeed())
		Expect(ValidTenantName("1team")).ToNot(Succeed())
		Expect(ValidTenantName("team/x")).ToNot(Succeed())
		Expect(ValidTenantName("Common")).ToNot(Succeed())
		Expect(ValidTenantName("Shared")).ToNot(Succeed())
		Expect(ValidTenantName("a2345678901234567890123456789012345678901234567890123456789012345")).ToNot(Succeed())
	})
})
