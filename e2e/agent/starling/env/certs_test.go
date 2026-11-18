//go:build e2e

package env_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/e2e/agent/starling/env"
)

func parseCert(certPem []byte) *x509.Certificate {
	GinkgoHelper()
	block, _ := pem.Decode(certPem)
	Expect(block).ToNot(BeNil())
	cert, err := x509.ParseCertificate(block.Bytes)
	Expect(err).ToNot(HaveOccurred())
	return cert
}

var _ = Describe("Certs", func() {
	var (
		certs *Certs
		pool  *x509.CertPool
	)

	BeforeEach(func() {
		var err error
		certs, err = NewCerts("controller.e2e.local", "172.31.250.100")
		Expect(err).ToNot(HaveOccurred())
		pool = x509.NewCertPool()
		Expect(pool.AppendCertsFromPEM(certs.CAPem)).To(BeTrue())
	})

	verify := func(certPem []byte, name string, usage x509.ExtKeyUsage) error {
		_, err := parseCert(certPem).Verify(x509.VerifyOptions{Roots: pool, DNSName: name, KeyUsages: []x509.ExtKeyUsage{usage}})
		return err
	}

	It("issues each agent a certificate named after it, valid as server and client", func() {
		agentCertPem, _, err := certs.AgentCert("agent-a")
		Expect(err).ToNot(HaveOccurred())

		Expect(parseCert(agentCertPem).Subject.CommonName).To(Equal("agent-a"))
		for _, name := range []string{AgentServerName, "127.0.0.1", "172.31.250.100", "localhost"} {
			Expect(verify(agentCertPem, name, x509.ExtKeyUsageServerAuth)).To(Succeed(), name)
		}
		Expect(verify(agentCertPem, "127.0.0.1", x509.ExtKeyUsageClientAuth)).To(Succeed())
		Expect(verify(agentCertPem, "172.31.250.99", x509.ExtKeyUsageServerAuth)).ToNot(Succeed())
	})

	It("issues a controller server certificate for its server name", func() {
		Expect(verify(certs.ControllerCertPem, "controller.e2e.local", x509.ExtKeyUsageServerAuth)).To(Succeed())
		Expect(verify(certs.ControllerCertPem, "other.e2e.local", x509.ExtKeyUsageServerAuth)).ToNot(Succeed())
	})

	It("issues leaf certificates for sni", func() {
		certPem, keyPem, err := certs.LeafCert("a", "a.e2e.local")
		Expect(err).ToNot(HaveOccurred())
		Expect(verify(certPem, "a.e2e.local", x509.ExtKeyUsageServerAuth)).To(Succeed())
		Expect(parseCert(certPem).Subject.CommonName).To(Equal("a"))
		_, err = tls.X509KeyPair(certPem, keyPem)
		Expect(err).ToNot(HaveOccurred())
	})

	It("requires the agent client certificate on the controller side", func() {
		lis, err := tls.Listen("tcp", "127.0.0.1:0", certs.ControllerTLSConfig())
		Expect(err).ToNot(HaveOccurred())
		defer lis.Close()
		go func() {
			for {
				conn, err := lis.Accept()
				if err != nil {
					return
				}
				_ = conn.(*tls.Conn).Handshake()
				_, _ = conn.Write([]byte("ok"))
				conn.Close()
			}
		}()

		dial := func(withClientCert bool) error {
			conf := &tls.Config{RootCAs: pool, ServerName: "controller.e2e.local"}
			if withClientCert {
				agentCertPem, agentKeyPem, err := certs.AgentCert("agent-a")
				Expect(err).ToNot(HaveOccurred())
				clientCert, err := tls.X509KeyPair(agentCertPem, agentKeyPem)
				Expect(err).ToNot(HaveOccurred())
				conf.Certificates = []tls.Certificate{clientCert}
			}
			conn, err := tls.Dial("tcp", lis.Addr().(*net.TCPAddr).String(), conf)
			if err != nil {
				return err
			}
			defer conn.Close()
			buf := make([]byte, 2)
			_, err = conn.Read(buf)
			return err
		}
		Expect(dial(true)).To(Succeed())
		Expect(dial(false)).ToNot(Succeed())
	})
})
