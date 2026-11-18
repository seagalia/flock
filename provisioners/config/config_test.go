package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

const baseConfig = `
controller:
  addr: 127.0.0.1:8443
  instance_uuid: instance-1
tls:
  cert_file: /tmp/cert.pem
  key_file: /tmp/key.pem
server:
  listen_host: 127.0.0.1
`

func writeConfig(content string) string {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), "config.yml")
	Expect(os.WriteFile(path, []byte(content), 0600)).To(Succeed())
	return path
}

var _ = Describe("ReadConfig", func() {
	It("leaves acme disabled with its defaults when the config has no acme block", func() {
		cnf, err := config.ReadConfig(writeConfig(baseConfig))

		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.Acme.Enabled).To(BeFalse())
	})

	It("listens on 18002 when the config has no server block, 18000 and 18001 being the ports of a starling agent and its agent list", func() {
		cnf, err := config.ReadConfig(writeConfig(`
controller:
  addr: 127.0.0.1:8443
  instance_uuid: instance-1
tls:
  cert_file: /tmp/cert.pem
  key_file: /tmp/key.pem
`))

		Expect(err).NotTo(HaveOccurred())
		defaults, err := agentconfig.DefaultServerConfig()
		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.ServerConfig.ListenHost).NotTo(BeEmpty())
		Expect(cnf.ServerConfig.ListenHost).To(Equal(defaults.ListenHost))
		Expect(cnf.ServerConfig.ListenPort).To(BeEquivalentTo(18002))
	})

	It("listens on 18002 when the server block gives no port", func() {
		cnf, err := config.ReadConfig(writeConfig(baseConfig))

		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.ServerConfig.ListenHost).To(Equal("127.0.0.1"))
		Expect(cnf.ServerConfig.ListenPort).To(BeEquivalentTo(18002))
	})

	DescribeTable("refuses the identity keys the provisioner no longer reads",
		func(read func(string) (*config.Config, error), content, message string) {
			_, err := read(writeConfig(content))

			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("node_name", config.ReadConfig, "node_name: prov-1\n"+baseConfig,
			"the name of the provisioner is the common name of its certificate (tls.cert_file)"),
		Entry("node_name read strictly", config.ReadConfigStrict, "node_name: prov-1\n"+baseConfig,
			"the name of the provisioner is the common name of its certificate (tls.cert_file)"),
		Entry("controller.agent_uuid", config.ReadConfig, strings.Replace(baseConfig, "controller:\n", "controller:\n  agent_uuid: agent-1\n", 1),
			"the controller gives the uuid of the provisioner"),
		Entry("controller.agent_uuid read strictly", config.ReadConfigStrict, strings.Replace(baseConfig, "controller:\n", "controller:\n  agent_uuid: agent-1\n", 1),
			"the controller gives the uuid of the provisioner"),
	)

	It("gives the acme block its defaults", func() {
		cnf, err := config.ReadConfig(writeConfig(baseConfig + `
acme:
  enabled: true
  challenges:
    http01_enabled: true
`))

		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.Acme.DefaultCADirUrl).To(Equal("https://acme-v02.api.letsencrypt.org/directory"))
		Expect(cnf.Acme.Challenges.Host).To(Equal("0.0.0.0"))
		Expect(cnf.Acme.Challenges.Http01Port).To(BeEquivalentTo(80))
		Expect(cnf.Acme.Challenges.TlsAlpn01Port).To(BeEquivalentTo(443))
	})

	It("reads the skip of the dns-01 propagation check, off by default", func() {
		cnf, err := config.ReadConfigStrict(writeConfig(baseConfig + `
acme:
  enabled: true
  challenges:
    dns01_skip_propagation_check: true
`))
		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.Acme.Challenges.Dns01SkipPropagationCheck).To(BeTrue())

		cnf, err = config.ReadConfig(writeConfig(baseConfig + "acme:\n  enabled: true\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.Acme.Challenges.Dns01SkipPropagationCheck).To(BeFalse())
	})

	It("reads the ttl of the leases, a minute by default", func() {
		cnf, err := config.ReadConfigStrict(writeConfig(baseConfig + "acme:\n  enabled: true\n  lease_ttl: 10s\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.Acme.LeaseTTL).To(Equal(10 * time.Second))

		cnf, err = config.ReadConfig(writeConfig(baseConfig + "acme:\n  enabled: true\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.Acme.LeaseTTL).To(Equal(time.Minute))
	})

	DescribeTable("refuses a ttl of the leases outside 10s and 5m",
		func(ttl string) {
			_, err := config.ReadConfig(writeConfig(baseConfig + "acme:\n  enabled: true\n  lease_ttl: " + ttl + "\n"))

			Expect(err).To(MatchError(And(ContainSubstring("lease_ttl"), ContainSubstring("10s"), ContainSubstring("5m0s"))))
		},
		Entry("negative", "-1s"),
		Entry("under 10s", "5s"),
		Entry("over 5m", "6m"),
	)

	It("refuses http-01 and tls-alpn-01 on the same port", func() {
		_, err := config.ReadConfig(writeConfig(baseConfig + `
acme:
  enabled: true
  challenges:
    http01_enabled: true
    tls_alpn_01_enabled: true
    http_port: 5002
    tls_port: 5002
`))

		Expect(err).To(MatchError(ContainSubstring("ports must be different")))
	})

	It("requires the controller and the tls blocks", func() {
		_, err := config.ReadConfig(writeConfig("acme:\n  enabled: false\n"))

		Expect(err).To(MatchError(ContainSubstring("controller config is required")))
	})

	It("refuses an unknown key only when read strictly", func() {
		path := writeConfig(baseConfig + "acme:\n  enabeld: true\n")

		_, err := config.ReadConfig(path)
		Expect(err).NotTo(HaveOccurred())

		_, err = config.ReadConfigStrict(path)
		Expect(err).To(MatchError(ContainSubstring("enabeld")))
	})
})

var _ = Describe("docker block", func() {
	It("leaves docker disabled when the config has no docker block", func() {
		cnf, err := config.ReadConfig(writeConfig(baseConfig))

		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.Docker.Enabled).To(BeFalse())
	})

	It("gives the docker block its defaults, the eds advertised on localhost as flock dev runs it", func() {
		cnf, err := config.ReadConfigStrict(writeConfig(baseConfig + `
docker:
  enabled: true
`))

		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.Docker.Host).To(BeEmpty())
		Expect(cnf.Docker.Vip).To(Equal("local"))
		Expect(cnf.Docker.HttpPort).To(BeEquivalentTo(80))
		Expect(cnf.Docker.HttpsPort).To(BeEquivalentTo(443))
		Expect(cnf.Docker.Eds.ListenHost).To(Equal("0.0.0.0"))
		Expect(cnf.Docker.Eds.ListenPort).To(BeEquivalentTo(18003))
		Expect(cnf.Docker.Eds.AdvertiseUrl).To(Equal("grpcs://localhost:18003"))
		Expect(cnf.Docker.DefaultDomain).To(BeEmpty())
	})

	It("drops the leading dot of the default domain", func() {
		cnf, err := config.ReadConfig(writeConfig(baseConfig + "docker:\n  enabled: true\n  default_domain: .apps.example.com\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.Docker.DefaultDomain).To(Equal("apps.example.com"))
	})

	It("advertises the eds on its own listen port by default, and keeps a given url", func() {
		cnf, err := config.ReadConfig(writeConfig(baseConfig + "docker:\n  enabled: true\n  eds:\n    listen_port: 19003\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.Docker.Eds.AdvertiseUrl).To(Equal("grpcs://localhost:19003"))

		cnf, err = config.ReadConfig(writeConfig(baseConfig + `
docker:
  enabled: true
  eds:
    advertise_url: grpc://provisioner.example.internal:18003
`))
		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.Docker.Eds.AdvertiseUrl).To(Equal("grpc://provisioner.example.internal:18003"))
	})

	DescribeTable("refuses an enabled docker block the agents could not use",
		func(block, message string) {
			_, err := config.ReadConfig(writeConfig(baseConfig + block))

			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("an http advertise url", "docker:\n  enabled: true\n  eds:\n    advertise_url: https://prov:18003\n",
			"must be grpc:// or grpcs://"),
		Entry("an advertise url with no host", "docker:\n  enabled: true\n  eds:\n    advertise_url: grpc://:18003\n",
			"has no host"),
		Entry("the eds on the server port", "docker:\n  enabled: true\n  eds:\n    listen_port: 18002\n    advertise_url: grpc://prov:18002\n",
			"is the port of the server"),
		Entry("http and https on one port", "docker:\n  enabled: true\n  http_port: 8080\n  https_port: 8080\n  eds:\n    advertise_url: grpc://prov:18003\n",
			"must be different"),
	)

	It("turns the docker block on from FLOCK_PROVISIONER_CONFIG_PATCH, refusing a misspelled key in strict mode", func() {
		path := writeConfig(baseConfig)
		GinkgoT().Setenv("FLOCK_PROVISIONER_CONFIG_PATCH", "docker:\n  enabled: true\n")

		cnf, err := config.ReadConfigStrict(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.Docker.Enabled).To(BeTrue())
		Expect(cnf.Docker.Eds.AdvertiseUrl).To(Equal("grpcs://localhost:18003"))

		GinkgoT().Setenv("FLOCK_PROVISIONER_CONFIG_PATCH", "docker:\n  enabeld: true\n")
		_, err = config.ReadConfigStrict(path)
		Expect(err).To(MatchError(ContainSubstring("enabeld")))
	})

	It("checks nothing of a disabled docker block", func() {
		_, err := config.ReadConfig(writeConfig(baseConfig + "docker:\n  enabled: false\n"))

		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("MakeProvisionerFilters", func() {
	It("matches the resources whose metadata names the provisioner", func() {
		filters := config.MakeProvisionerFilters(config.ACMEProvisionerName)

		Expect(filters).To(HaveLen(1))
		Expect(filters[0].GetKey()).To(Equal("flock.provisioner.acme"))
		Expect(filters[0].GetValue()).To(Equal("true"))
		Expect(filters[0].GetOperator()).To(Equal(svccore.ListFilter_EQUAL))
		Expect(filters[0].GetIsMetadata()).To(BeTrue())
	})
})
