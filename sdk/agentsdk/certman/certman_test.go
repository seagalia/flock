package certman_test

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/agentsdk/certman"
)

var _ = Describe("CertMan", func() {
	var (
		certFile, keyFile, caFile string
		otherCertFile             string
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		certFile = filepath.Join(dir, "cert.pem")
		keyFile = filepath.Join(dir, "key.pem")
		caFile = filepath.Join(dir, "ca.pem")
		otherCertFile = filepath.Join(dir, "other-cert.pem")
		writeKeyPair(certFile, keyFile, 1)
		writeKeyPair(caFile, filepath.Join(dir, "ca-key.pem"), 100)
	})

	serialOf := func(cm *CertMan) int64 {
		keyPair, err := cm.GetCertificate(&tls.ClientHelloInfo{})
		Expect(err).ToNot(HaveOccurred())
		if keyPair == nil {
			return 0
		}
		leaf, err := x509.ParseCertificate(keyPair.Certificate[0])
		Expect(err).ToNot(HaveOccurred())
		return leaf.SerialNumber.Int64()
	}

	It("should give no certificate before watching", func() {
		cm, err := New(nil, certFile, keyFile, caFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(serialOf(cm)).To(BeZero())
	})

	It("should load the key pair when starting to watch", func() {
		cm, err := New(nil, certFile, keyFile, caFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(cm.Watch()).To(Succeed())
		DeferCleanup(cm.Stop)

		Expect(serialOf(cm)).To(BeEquivalentTo(1))
		clientCert, err := cm.GetClientCertificate(&tls.CertificateRequestInfo{})
		Expect(err).ToNot(HaveOccurred())
		Expect(clientCert).ToNot(BeNil())
	})

	It("should reload the key pair when files change", func() {
		cm, err := New(nil, certFile, keyFile, caFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(cm.Watch()).To(Succeed())
		DeferCleanup(cm.Stop)

		writeKeyPair(certFile, keyFile, 2)
		Eventually(func() int64 { return serialOf(cm) }, 5*time.Second).Should(BeEquivalentTo(2))
	})

	It("should keep the previous key pair when new files are invalid", func() {
		cm, err := New(nil, certFile, keyFile, caFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(cm.Watch()).To(Succeed())
		DeferCleanup(cm.Stop)

		Expect(os.WriteFile(certFile, []byte("not a cert"), 0600)).To(Succeed())
		Consistently(func() int64 { return serialOf(cm) }, 300*time.Millisecond).Should(BeEquivalentTo(1))
	})

	It("should append the ca to the cert pool when ca file changes", func() {
		certPool := x509.NewCertPool()
		cm, err := New(certPool, certFile, keyFile, caFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(cm.Watch()).To(Succeed())

		writeKeyPair(otherCertFile, filepath.Join(filepath.Dir(caFile), "other-key.pem"), 200)
		caContent, err := os.ReadFile(otherCertFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(os.WriteFile(caFile, caContent, 0600)).To(Succeed())

		// certman mutates the pool in its goroutine, stopping it first avoids a racy read
		time.Sleep(500 * time.Millisecond)
		cm.Stop()
		expectedPool := x509.NewCertPool()
		Expect(expectedPool.AppendCertsFromPEM(caContent)).To(BeTrue())
		Expect(certPool.Equal(expectedPool)).To(BeTrue())
	})

	It("should fail to watch missing files", func() {
		cm, err := New(nil, filepath.Join(filepath.Dir(certFile), "missing.pem"), keyFile, caFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(cm.Watch()).To(MatchError(ContainSubstring("cert file")))
	})

	It("should not reload files after being stopped", func() {
		cm, err := New(nil, certFile, keyFile, caFile)
		Expect(err).ToNot(HaveOccurred())
		Expect(cm.Watch()).To(Succeed())
		cm.Stop()

		writeKeyPair(certFile, keyFile, 3)
		Consistently(func() int64 { return serialOf(cm) }, 300*time.Millisecond).Should(BeEquivalentTo(1))
	})
})
