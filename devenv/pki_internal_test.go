package devenv

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ensurePki", func() {
	var certs *pki

	BeforeEach(func() {
		var err error
		certs, err = ensurePki(GinkgoT().TempDir(), nil)
		Expect(err).ToNot(HaveOccurred())
	})

	verify := func(certFile, keyFile string, usage x509.ExtKeyUsage, name string) {
		GinkgoHelper()
		_, err := tls.LoadX509KeyPair(certFile, keyFile)
		Expect(err).ToNot(HaveOccurred())

		caPem, err := os.ReadFile(certs.CaFile)
		Expect(err).ToNot(HaveOccurred())
		roots := x509.NewCertPool()
		Expect(roots.AppendCertsFromPEM(caPem)).To(BeTrue())

		certPem, err := os.ReadFile(certFile)
		Expect(err).ToNot(HaveOccurred())
		block, _ := pem.Decode(certPem)
		cert, err := x509.ParseCertificate(block.Bytes)
		Expect(err).ToNot(HaveOccurred())
		_, err = cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: name, KeyUsages: []x509.ExtKeyUsage{usage}})
		Expect(err).ToNot(HaveOccurred())
	}

	It("should issue a controller server certificate for localhost and 127.0.0.1", func() {
		verify(certs.ControllerCertFile, certs.ControllerKeyFile, x509.ExtKeyUsageServerAuth, "localhost")
		verify(certs.ControllerCertFile, certs.ControllerKeyFile, x509.ExtKeyUsageServerAuth, "127.0.0.1")
	})

	It("should issue the client certificate controller dials agents with", func() {
		verify(certs.ControllerAgentCertFile, certs.ControllerAgentKeyFile, x509.ExtKeyUsageClientAuth, "")
	})

	commonName := func(certFile string) string {
		GinkgoHelper()
		certPem, err := os.ReadFile(certFile)
		Expect(err).ToNot(HaveOccurred())
		block, _ := pem.Decode(certPem)
		cert, err := x509.ParseCertificate(block.Bytes)
		Expect(err).ToNot(HaveOccurred())
		return cert.Subject.CommonName
	}

	It("should issue an agent certificate named local, serving flock_agent and authenticating on controller", func() {
		verify(certs.AgentCertFile, certs.AgentKeyFile, x509.ExtKeyUsageServerAuth, agentServerName)
		verify(certs.AgentCertFile, certs.AgentKeyFile, x509.ExtKeyUsageServerAuth, "localhost")
		verify(certs.AgentCertFile, certs.AgentKeyFile, x509.ExtKeyUsageClientAuth, "")
		Expect(commonName(certs.AgentCertFile)).To(Equal("local"))
	})

	It("should issue a provisioner certificate named local-provisioner, serving flock_agent and authenticating on controller", func() {
		verify(certs.ProvisionerCertFile, certs.ProvisionerKeyFile, x509.ExtKeyUsageServerAuth, agentServerName)
		verify(certs.ProvisionerCertFile, certs.ProvisionerKeyFile, x509.ExtKeyUsageServerAuth, "localhost")
		verify(certs.ProvisionerCertFile, certs.ProvisionerKeyFile, x509.ExtKeyUsageClientAuth, "")
		Expect(commonName(certs.ProvisionerCertFile)).To(Equal("local-provisioner"))
	})

	readFile := func(path string) []byte {
		GinkgoHelper()
		content, err := os.ReadFile(path)
		Expect(err).ToNot(HaveOccurred())
		return content
	}

	It("should keep the CA, the leaves and the jwt keys across two calls", func() {
		dir := filepath.Dir(certs.CaFile)
		before := map[string][]byte{}
		files, err := os.ReadDir(dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(files).ToNot(BeEmpty())
		for _, file := range files {
			before[file.Name()] = readFile(filepath.Join(dir, file.Name()))
		}
		Expect(before).To(HaveKey("ca-key.pem"))
		Expect(before).To(HaveKey("jwt-private.pem"))
		Expect(before).To(HaveKey("jwt-public.pem"))

		again, err := ensurePki(dir, nil)
		Expect(err).ToNot(HaveOccurred())

		for name, content := range before {
			Expect(readFile(filepath.Join(dir, name))).To(Equal(content), name)
		}
		Expect(again.JwtPrivateKeyPem).To(Equal(certs.JwtPrivateKeyPem))
		Expect(again.JwtPublicKeyPem).To(Equal(certs.JwtPublicKeyPem))
	})

	It("should issue a missing leaf with the stored CA", func() {
		dir := filepath.Dir(certs.CaFile)
		caBefore := readFile(certs.CaFile)
		Expect(os.Remove(certs.ProvisionerCertFile)).To(Succeed())

		var err error
		certs, err = ensurePki(dir, nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(readFile(certs.CaFile)).To(Equal(caBefore))
		verify(certs.ProvisionerCertFile, certs.ProvisionerKeyFile, x509.ExtKeyUsageClientAuth, "")
	})

	It("should reissue only the controller certificate when a name is added", func() {
		dir := filepath.Dir(certs.CaFile)
		caBefore := readFile(certs.CaFile)
		agentBefore := readFile(certs.AgentCertFile)
		controllerBefore := readFile(certs.ControllerCertFile)

		var err error
		certs, err = ensurePki(dir, []string{"flock.example.com", "10.1.2.3"})
		Expect(err).ToNot(HaveOccurred())

		verify(certs.ControllerCertFile, certs.ControllerKeyFile, x509.ExtKeyUsageServerAuth, "flock.example.com")
		verify(certs.ControllerCertFile, certs.ControllerKeyFile, x509.ExtKeyUsageServerAuth, "10.1.2.3")
		verify(certs.ControllerCertFile, certs.ControllerKeyFile, x509.ExtKeyUsageServerAuth, "localhost")
		Expect(readFile(certs.ControllerCertFile)).ToNot(Equal(controllerBefore))
		Expect(readFile(certs.CaFile)).To(Equal(caBefore))
		Expect(readFile(certs.AgentCertFile)).To(Equal(agentBefore))
	})

	It("should keep the controller certificate when the names are already covered", func() {
		dir := filepath.Dir(certs.CaFile)
		_, err := ensurePki(dir, []string{"flock.example.com", "::1"})
		Expect(err).ToNot(HaveOccurred())
		controllerBefore := readFile(certs.ControllerCertFile)

		_, err = ensurePki(dir, []string{"flock.example.com"})
		Expect(err).ToNot(HaveOccurred())

		Expect(readFile(certs.ControllerCertFile)).To(Equal(controllerBefore))
	})

	It("should ignore empty names and keep the controller certificate", func() {
		dir := filepath.Dir(certs.CaFile)
		controllerBefore := readFile(certs.ControllerCertFile)

		_, err := ensurePki(dir, []string{""})
		Expect(err).ToNot(HaveOccurred())

		Expect(readFile(certs.ControllerCertFile)).To(Equal(controllerBefore))
	})

	It("should regenerate everything when the CA key is missing", func() {
		dir := filepath.Dir(certs.CaFile)
		caBefore := readFile(certs.CaFile)
		Expect(os.Remove(filepath.Join(dir, "ca-key.pem"))).To(Succeed())

		var err error
		certs, err = ensurePki(dir, nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(readFile(certs.CaFile)).ToNot(Equal(caBefore))
		verify(certs.AgentCertFile, certs.AgentKeyFile, x509.ExtKeyUsageClientAuth, "")
	})

	It("should regenerate everything when the CA expires within 30 days", func() {
		dir := filepath.Dir(certs.CaFile)
		writeShortLivedCa(dir, 10*24*time.Hour)
		caBefore := readFile(certs.CaFile)

		var err error
		certs, err = ensurePki(dir, nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(readFile(certs.CaFile)).ToNot(Equal(caBefore))
		verify(certs.AgentCertFile, certs.AgentKeyFile, x509.ExtKeyUsageClientAuth, "")
	})

	It("should refuse a corrupt CA key", func() {
		dir := filepath.Dir(certs.CaFile)
		Expect(os.WriteFile(filepath.Join(dir, "ca-key.pem"), []byte("garbage"), 0600)).To(Succeed())

		_, err := ensurePki(dir, nil)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("ca-key.pem"))
		Expect(err.Error()).To(ContainSubstring("remove"))
	})

	It("should give a jwt key pair", func() {
		Expect(certs.JwtPrivateKeyPem).To(HavePrefix("-----BEGIN PRIVATE KEY-----"))
		Expect(certs.JwtPublicKeyPem).To(HavePrefix("-----BEGIN PUBLIC KEY-----"))
	})
})

// writeShortLivedCa replaces the CA of dir with one expiring after validity, keeping its leaves
func writeShortLivedCa(dir string, validity time.Duration) {
	GinkgoHelper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).ToNot(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "flock-dev-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(validity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).ToNot(HaveOccurred())
	keyDer, err := x509.MarshalECPrivateKey(key)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(dir, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(dir, "ca-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}), 0600)).To(Succeed())
}
