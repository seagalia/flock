package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	. "github.com/seagalia/flock/controller/config"
	"go.yaml.in/yaml/v2"
)

func writeFile(dir, name string) string {
	path := filepath.Join(dir, name)
	err := os.WriteFile(path, []byte("content"), 0600)
	Expect(err).ToNot(HaveOccurred())
	return path
}

var _ = Describe("Config", func() {
	var certFile, keyFile, caFile string
	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		certFile = writeFile(dir, "cert.pem")
		keyFile = writeFile(dir, "key.pem")
		caFile = writeFile(dir, "ca.pem")
	})

	mtlsYaml := func() string {
		return fmt.Sprintf("mtls:\n  cert_file: %s\n  key_file: %s\n", certFile, keyFile)
	}
	serverYaml := "server:\n  tls:\n    cert_file: cert\n    key_file: key\n" +
		"  login: {type: basic, basic_auth_options: [{username: u, password: p}]}\n" +
		"  jwt_maker: {alg: HS256, private_key: secret}\n"
	storeYaml := "store:\n  sql: {type: sqlite, dsn: flock.db, secret_key: s}\n"

	Context("UnmarshalYAML", func() {
		It("should load a minimal config with defaults", func() {
			cnf := &Config{}
			err := yaml.Unmarshal([]byte(mtlsYaml()+serverYaml+storeYaml), cnf)
			Expect(err).ToNot(HaveOccurred())

			Expect(cnf.Server.ListenAddr).To(Equal(":8080"))
			Expect(cnf.Mtls.ServerName).To(Equal("flock_agent"))
		})
		When("mtls is missing", func() {
			It("should return an error", func() {
				err := yaml.Unmarshal([]byte(serverYaml), &Config{})
				Expect(err).To(MatchError(ContainSubstring("mtls is empty")))
			})
		})
		When("server is missing", func() {
			It("should return an error", func() {
				err := yaml.Unmarshal([]byte(mtlsYaml()), &Config{})
				Expect(err).To(MatchError(ContainSubstring("server is empty")))
			})
		})
		When("store is missing", func() {
			It("should return an error", func() {
				err := yaml.Unmarshal([]byte(mtlsYaml()+serverYaml), &Config{})
				Expect(err).To(MatchError(ContainSubstring("store.sql is required")))
			})
		})
		When("store has no sql", func() {
			It("should return an error", func() {
				err := yaml.Unmarshal([]byte(mtlsYaml()+serverYaml+"store: {}\n"), &Config{})
				Expect(err).To(MatchError(ContainSubstring("store.sql is required")))
			})
		})
		When("two instantiators have the same name", func() {
			It("should return an error", func() {
				instantiator := `
  - name: same
    zones: [z1]
    resource_types: [{name: small}]
    static: {agents: [{uuid: u, name: n, host: h}]}
`
				content := mtlsYaml() + serverYaml + storeYaml + "instantiators:" + instantiator + instantiator
				err := yaml.Unmarshal([]byte(content), &Config{})
				Expect(err).To(MatchError(ContainSubstring("duplicate name: same")))
			})
		})
	})

	Context("ReadConfig and WriteConfig", func() {
		It("should read back a written config", func() {
			cnf := &Config{}
			err := yaml.Unmarshal([]byte(mtlsYaml()+serverYaml+storeYaml), cnf)
			Expect(err).ToNot(HaveOccurred())
			path := filepath.Join(GinkgoT().TempDir(), "config.yml")

			err = WriteConfig(cnf, path)
			Expect(err).ToNot(HaveOccurred())
			readCnf, err := ReadConfig(path)
			Expect(err).ToNot(HaveOccurred())

			Expect(readCnf.Mtls.CertFile).To(Equal(certFile))
			Expect(readCnf.Server.Tls.CertFile).To(Equal("cert"))
		})
		When("file does not exist", func() {
			It("should return an error", func() {
				_, err := ReadConfig(filepath.Join(GinkgoT().TempDir(), "missing.yml"))
				Expect(err).To(HaveOccurred())
			})
		})
		It("merges the yaml of FLOCK_CONTROLLER_CONFIG_PATCH over the file, refusing its unknown keys in strict mode", func() {
			path := filepath.Join(GinkgoT().TempDir(), "config.yml")
			Expect(os.WriteFile(path, []byte(mtlsYaml()+serverYaml+storeYaml), 0600)).To(Succeed())
			GinkgoT().Setenv("FLOCK_CONTROLLER_CONFIG_PATCH", "server:\n  listen_addr: :9090\n")

			cnf, err := ReadConfigStrict(path)
			Expect(err).ToNot(HaveOccurred())
			Expect(cnf.Server.ListenAddr).To(Equal(":9090"))
			Expect(cnf.Server.Tls.CertFile).To(Equal("cert"))

			GinkgoT().Setenv("FLOCK_CONTROLLER_CONFIG_PATCH", "sever: {}\n")
			_, err = ReadConfigStrict(path)
			Expect(err).To(MatchError(ContainSubstring("field sever not found")))
		})
		It("should refuse unknown keys in strict mode only", func() {
			path := filepath.Join(GinkgoT().TempDir(), "config.yml")
			Expect(os.WriteFile(path, []byte(mtlsYaml()+serverYaml+storeYaml+"sever: {}\n"), 0600)).To(Succeed())

			_, err := ReadConfig(path)
			Expect(err).ToNot(HaveOccurred())
			_, err = ReadConfigStrict(path)
			Expect(err).To(MatchError(ContainSubstring("field sever not found")))
		})
		DescribeTable("refuses the old keys in strict mode",
			func(content string, field string) {
				path := filepath.Join(GinkgoT().TempDir(), "config.yml")
				Expect(os.WriteFile(path, []byte(mtlsYaml()+content+storeYaml), 0600)).To(Succeed())

				_, err := ReadConfigStrict(path)
				Expect(err).To(MatchError(ContainSubstring("field " + field + " not found")))
			},
			Entry("login services", strings.Replace(serverYaml, "{type: basic,", "{services: [], type: basic,", 1), "services"),
			Entry("jwt validations", serverYaml+"  jwt_validations: []\n", "jwt_validations"),
			Entry("devui", serverYaml+"  devui: {username: u}\n", "devui"),
		)
		It("refuses the dynamic instantiator in strict mode", func() {
			instantiator := `
instantiators:
  - name: inst
    zones: [z1]
    resource_types: [{name: small}]
    dynamic: {host: localhost}
`
			path := filepath.Join(GinkgoT().TempDir(), "config.yml")
			Expect(os.WriteFile(path, []byte(mtlsYaml()+serverYaml+storeYaml+instantiator), 0600)).To(Succeed())

			_, err := ReadConfigStrict(path)
			Expect(err).To(MatchError(ContainSubstring("field dynamic not found")))
		})
		When("file is not valid yaml", func() {
			It("should return an error", func() {
				path := filepath.Join(GinkgoT().TempDir(), "config.yml")
				Expect(os.WriteFile(path, []byte("server: [unclosed"), 0600)).To(Succeed())

				_, err := ReadConfig(path)
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Context("Mtls", func() {
		It("should keep given server name", func() {
			mtls := &Mtls{}
			content := fmt.Sprintf("cert_file: %s\nkey_file: %s\nca_file: %s\nserver_name: my-agent\n", certFile, keyFile, caFile)
			err := yaml.Unmarshal([]byte(content), mtls)
			Expect(err).ToNot(HaveOccurred())
			Expect(mtls.ServerName).To(Equal("my-agent"))
		})
		DescribeTable("should reject invalid files",
			func(content func() string, errMsg string) {
				err := yaml.Unmarshal([]byte(content()), &Mtls{})
				Expect(err).To(MatchError(ContainSubstring(errMsg)))
			},
			Entry("cert file empty", func() string {
				return fmt.Sprintf("key_file: %s\n", keyFile)
			}, "cert file is empty"),
			Entry("key file empty", func() string {
				return fmt.Sprintf("cert_file: %s\n", certFile)
			}, "key file is empty"),
			Entry("cert file missing", func() string {
				return fmt.Sprintf("cert_file: /does/not/exist\nkey_file: %s\n", keyFile)
			}, "cert file not found"),
			Entry("key file missing", func() string {
				return fmt.Sprintf("cert_file: %s\nkey_file: /does/not/exist\n", certFile)
			}, "key file not found"),
			Entry("ca file missing", func() string {
				return fmt.Sprintf("cert_file: %s\nkey_file: %s\nca_file: /does/not/exist\n", certFile, keyFile)
			}, "ca file not found"),
		)
	})

	Context("IPAM", func() {
		It("should require the url of a remote ipam", func() {
			err := yaml.Unmarshal([]byte("insecure_skip_verify: true\n"), &RemoteIPAMConfig{})
			Expect(err).To(MatchError(ContainSubstring("url is required")))
		})

		It("should read remote headers with one or several values", func() {
			ipam := &IPAMConfig{}
			Expect(yaml.Unmarshal([]byte("remote:\n  url: https://ipam.local\n  headers:\n    Authorization: Bearer tok\n"), ipam)).To(Succeed())
			Expect(ipam.Remote.Headers).To(Equal(RemoteIPAMHeader{"Authorization": {"Bearer tok"}}))

			ipam = &IPAMConfig{}
			Expect(yaml.Unmarshal([]byte("remote:\n  url: https://ipam.local\n  headers:\n    X-Team: [a, b]\n"), ipam)).To(Succeed())
			Expect(ipam.Remote.Headers).To(Equal(RemoteIPAMHeader{"X-Team": {"a", "b"}}))
		})

		It("should refuse headers that are not values", func() {
			err := yaml.Unmarshal([]byte("url: https://ipam.local\nheaders:\n  X-Team: {a: b}\n"), &RemoteIPAMConfig{})
			Expect(err).To(MatchError(ContainSubstring("remote ipam headers")))
		})
	})

	Context("Log", func() {
		It("should read log settings without applying them", func() {
			levelBefore := zerolog.GlobalLevel()
			logConf := &Log{}
			Expect(yaml.Unmarshal([]byte("level: debug\nin_json: true\n"), logConf)).To(Succeed())
			Expect(logConf.Level).To(Equal("debug"))
			Expect(logConf.InJson).To(BeTrue())
			Expect(zerolog.GlobalLevel()).To(Equal(levelBefore))
		})
		It("should accept warning level", func() {
			Expect(yaml.Unmarshal([]byte("level: warning\n"), &Log{})).To(Succeed())
		})
		It("should reject unknown level", func() {
			err := yaml.Unmarshal([]byte("level: unknown\n"), &Log{})
			Expect(err).To(HaveOccurred())
		})
	})
})
