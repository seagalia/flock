//go:build e2e

package env_test

import (
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/e2e/controller/env"
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
		certs, err = NewCerts("controller.e2e.local", "controller-2.e2e.local")
		Expect(err).ToNot(HaveOccurred())
		pool = x509.NewCertPool()
		Expect(pool.AppendCertsFromPEM(certs.CAPem)).To(BeTrue())
	})

	verify := func(certPem []byte, name string, usage x509.ExtKeyUsage) error {
		_, err := parseCert(certPem).Verify(x509.VerifyOptions{Roots: pool, DNSName: name, KeyUsages: []x509.ExtKeyUsage{usage}})
		return err
	}

	It("issues a controller server certificate for every given name, localhost and 127.0.0.1", func() {
		for _, name := range []string{"controller.e2e.local", "controller-2.e2e.local", "localhost", "127.0.0.1"} {
			Expect(verify(certs.ControllerServerCertPem, name, x509.ExtKeyUsageServerAuth)).To(Succeed(), name)
		}
		Expect(verify(certs.ControllerServerCertPem, "other.e2e.local", x509.ExtKeyUsageServerAuth)).ToNot(Succeed())
		Expect(verify(certs.ControllerServerCertPem, "controller.e2e.local", x509.ExtKeyUsageClientAuth)).ToNot(Succeed())
	})

	It("issues an agent client certificate with CN agent-e2e", func() {
		Expect(verify(certs.SubscriberClientCertPem, "", x509.ExtKeyUsageClientAuth)).To(Succeed())
		Expect(parseCert(certs.SubscriberClientCertPem).Subject.CommonName).To(Equal("agent-e2e"))
	})

	It("issues an agent server certificate for flock_agent", func() {
		Expect(parseCert(certs.AgentServerCertPem).DNSNames).To(ConsistOf("flock_agent"))
		Expect(verify(certs.AgentServerCertPem, "flock_agent", x509.ExtKeyUsageServerAuth)).To(Succeed())
	})

	It("issues a client certificate for controller's mtls block", func() {
		Expect(verify(certs.ControllerAgentClientCertPem, "", x509.ExtKeyUsageClientAuth)).To(Succeed())
	})

	It("generates a matching rsa key pair for the jwt", func() {
		privBlock, _ := pem.Decode(certs.JwtPrivateKeyPem)
		Expect(privBlock).ToNot(BeNil())
		priv, err := x509.ParsePKCS8PrivateKey(privBlock.Bytes)
		Expect(err).ToNot(HaveOccurred())

		pubBlock, _ := pem.Decode(certs.JwtPublicKeyPem)
		Expect(pubBlock).ToNot(BeNil())
		pub, err := x509.ParsePKIXPublicKey(pubBlock.Bytes)
		Expect(err).ToNot(HaveOccurred())

		rsaKey := priv.(*rsa.PrivateKey)
		Expect(rsaKey.N.BitLen()).To(Equal(2048))
		Expect(rsaKey.PublicKey.Equal(pub)).To(BeTrue())
	})

	It("lets an agent server and controller's agent client handshake with mutual tls", func() {
		lis, err := tls.Listen("tcp", "127.0.0.1:0", certs.AgentTLSConfig())
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

		dial := func(conf *tls.Config) error {
			conn, err := tls.Dial("tcp", lis.Addr().(*net.TCPAddr).String(), conf)
			if err != nil {
				return err
			}
			defer conn.Close()
			_, err = conn.Read(make([]byte, 2))
			return err
		}
		withoutClientCert := &tls.Config{RootCAs: pool, ServerName: "flock_agent"}
		Expect(dial(certs.ControllerAgentTLSConfig())).To(Succeed())
		Expect(dial(withoutClientCert)).ToNot(Succeed())
	})

	It("requires the agent client certificate on controller's side but not for users", func() {
		controllerConf := &tls.Config{
			Certificates: []tls.Certificate{mustKeyPair(certs.ControllerServerCertPem, certs.ControllerServerKeyPem)},
			ClientAuth:   tls.VerifyClientCertIfGiven,
			ClientCAs:    pool,
		}
		lis, err := tls.Listen("tcp", "127.0.0.1:0", controllerConf)
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

		dial := func(conf *tls.Config) error {
			conn, err := tls.Dial("tcp", lis.Addr().(*net.TCPAddr).String(), conf)
			if err != nil {
				return err
			}
			defer conn.Close()
			_, err = conn.Read(make([]byte, 2))
			return err
		}
		Expect(dial(certs.SubscriberTLSConfig("controller.e2e.local"))).To(Succeed())
		Expect(dial(certs.UserTLSConfig("controller.e2e.local"))).To(Succeed())
		Expect(dial(certs.UserTLSConfig("other.e2e.local"))).ToNot(Succeed())
	})

	It("writes every file controller's config refers to, keys readable by the owner only", func() {
		dir := GinkgoT().TempDir()
		Expect(certs.WriteFiles(dir)).To(Succeed())

		certFiles := []string{"ca.pem", "controller.pem", "controller-agent.pem"}
		keyFiles := []string{"controller-key.pem", "controller-agent-key.pem", "jwt.pem"}
		for _, name := range append(certFiles, "jwt.pub") {
			content, err := os.ReadFile(filepath.Join(dir, name))
			Expect(err).ToNot(HaveOccurred(), name)
			block, _ := pem.Decode(content)
			Expect(block).ToNot(BeNil(), name)
		}
		for _, name := range certFiles {
			content, _ := os.ReadFile(filepath.Join(dir, name))
			parseCert(content)
		}
		for _, name := range keyFiles {
			info, err := os.Stat(filepath.Join(dir, name))
			Expect(err).ToNot(HaveOccurred(), name)
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)), name)
		}
		controllerCert, _ := os.ReadFile(filepath.Join(dir, "controller.pem"))
		controllerKey, _ := os.ReadFile(filepath.Join(dir, "controller-key.pem"))
		_, err := tls.X509KeyPair(controllerCert, controllerKey)
		Expect(err).ToNot(HaveOccurred())
	})
})

func mustKeyPair(certPem, keyPem []byte) tls.Certificate {
	GinkgoHelper()
	pair, err := tls.X509KeyPair(certPem, keyPem)
	Expect(err).ToNot(HaveOccurred())
	return pair
}
