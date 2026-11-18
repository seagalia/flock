package agentconfig_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/agentsdk/agentconfig"
)

// writeCertificate writes a self-signed certificate with commonName as CN and gives its path
func writeCertificate(commonName string) string {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).ToNot(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).ToNot(HaveOccurred())
	certFile := filepath.Join(GinkgoT().TempDir(), "cert.pem")
	Expect(os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)).To(Succeed())
	return certFile
}

var _ = Describe("CommonName", func() {
	It("should give the common name of the certificate", func() {
		Expect(CommonName(writeCertificate("edge-1"))).To(Equal("edge-1"))
	})

	It("should refuse a common name breaking the agent name rule", func() {
		_, err := CommonName(writeCertificate("Edge_1"))
		Expect(err).To(MatchError(ContainSubstring(`"Edge_1"`)))
	})

	It("should skip the blocks before the first certificate", func() {
		certFile := writeCertificate("edge-1")
		content, err := os.ReadFile(certFile)
		Expect(err).ToNot(HaveOccurred())
		keyBlock := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte("not read")})
		Expect(os.WriteFile(certFile, append(keyBlock, content...), 0o600)).To(Succeed())
		Expect(CommonName(certFile)).To(Equal("edge-1"))
	})

	It("should fail on a file holding no certificate", func() {
		notCert := filepath.Join(GinkgoT().TempDir(), "cert.pem")
		Expect(os.WriteFile(notCert, []byte("not a certificate"), 0o600)).To(Succeed())
		_, err := CommonName(notCert)
		Expect(err).To(HaveOccurred())
	})

	It("should fail on a missing file", func() {
		_, err := CommonName(filepath.Join(GinkgoT().TempDir(), "missing.pem"))
		Expect(err).To(HaveOccurred())
	})
})
