package devenv

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Provisioner config", func() {
	var (
		certs *pki
		path  string
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		var err error
		certs, err = ensurePki(filepath.Join(dir, "pki"), nil)
		Expect(err).ToNot(HaveOccurred())
		path = filepath.Join(dir, "provisioner.yml")
	})

	It("should write the defaults when the file is missing", func() {
		cnf, err := ensureProvisionerConfig(certs, "10.0.0.5:8443", "instance-uuid", path, "debug")
		Expect(err).ToNot(HaveOccurred())
		Expect(path).To(BeAnExistingFile())

		Expect(cnf.Log.Level).To(Equal("debug"))
		Expect(cnf.ControllerConfig.Addr).To(Equal("10.0.0.5:8443"))
		Expect(cnf.ControllerConfig.ServerName).To(Equal("localhost"))
		Expect(cnf.ControllerConfig.InstanceUuid).To(Equal("instance-uuid"))
		Expect(cnf.TlsConfig.CertFile).To(Equal(certs.ProvisionerCertFile))
		Expect(cnf.TlsConfig.KeyFile).To(Equal(certs.ProvisionerKeyFile))
		Expect(cnf.TlsConfig.CaFile).To(Equal(certs.CaFile))
		Expect(cnf.ServerConfig.ListenHost).To(Equal("127.0.0.1"))
		Expect(cnf.ServerConfig.ListenPort).To(BeEquivalentTo(18002))

		Expect(cnf.Acme.Enabled).To(BeTrue())
		Expect(cnf.Acme.DefaultCADirUrl).To(Equal("https://acme-staging-v02.api.letsencrypt.org/directory"))
		Expect(cnf.Acme.Challenges.Http01Enabled).To(BeFalse())
		Expect(cnf.Acme.Challenges.TlsAlpn01Enabled).To(BeFalse())
		Expect(cnf.Acme.Challenges.Host).To(Equal("0.0.0.0"))
		Expect(cnf.Acme.Challenges.Http01Port).To(BeEquivalentTo(5002))
		Expect(cnf.Acme.Challenges.TlsAlpn01Port).To(BeEquivalentTo(5001))
	})

	It("should keep the file of the user as it is", func() {
		userConfig := []byte(`controller:
  addr: 127.0.0.1:8443
  instance_uuid: user-instance
tls:
  cert_file: /user/cert.pem
  key_file: /user/key.pem
log:
  level: error
acme:
  enabled: false
`)
		Expect(os.WriteFile(path, userConfig, 0600)).To(Succeed())

		cnf, err := ensureProvisionerConfig(certs, "10.0.0.6:8443", "instance-uuid", path, "debug")
		Expect(err).ToNot(HaveOccurred())

		Expect(cnf.ControllerConfig.Addr).To(Equal("10.0.0.6:8443"), "the controller address follows --listen-host")
		written, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		Expect(written).To(Equal(userConfig))
		Expect(cnf.ControllerConfig.InstanceUuid).To(Equal("user-instance"))
		Expect(cnf.TlsConfig.CertFile).To(Equal("/user/cert.pem"))
		Expect(cnf.Acme.Enabled).To(BeFalse())
		Expect(cnf.Log.Level).To(Equal("debug"), "flock dev sets the logging of every app of the process")
	})
})

var _ = Describe("startProvisioner", func() {
	It("should return from its stop function once the given ctx is cancelled", func() {
		dir := GinkgoT().TempDir()
		certs, err := ensurePki(filepath.Join(dir, "pki"), nil)
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		// a free port, so the spec can't clash with a running flock dev; the file is then read as the user's
		path := filepath.Join(dir, "provisioner.yml")
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		freePort := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
		Expect(listener.Close()).To(Succeed())
		_, err = ensureProvisionerConfig(certs, "10.0.0.5:8443", "instance-uuid", path, "error")
		Expect(err).ToNot(HaveOccurred())
		written, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		rewritten := strings.Replace(string(written), "listen_port: "+strconv.Itoa(provisionerListenPort), "listen_port: "+freePort, 1)
		Expect(os.WriteFile(path, []byte(rewritten), 0600)).To(Succeed())

		wait := startProvisioner(ctx, certs, "10.0.0.5:8443", "instance-uuid", path, "error", semver.MustParse("1.0.0"))
		Expect(wait).ToNot(BeNil())

		cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			wait()
		}()
		Eventually(done, 15*time.Second).Should(BeClosed())
	})
})
