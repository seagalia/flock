package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Masterminds/semver/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v2"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/agent/f5/bigip/bigipmock"
	"github.com/seagalia/flock/agent/f5/config"
	f5_backendopts "github.com/seagalia/flock/sdk/flock/extensions/backend/f5_options/v1"
	f5_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/f5_options/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	"github.com/seagalia/flock/sdk/helpers"
)

func writeSelfSigned(dir, commonName string) (certFile, keyFile string) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).ToNot(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: commonName},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).ToNot(HaveOccurred())
	keyDer, err := x509.MarshalECPrivateKey(key)
	Expect(err).ToNot(HaveOccurred())
	certFile, keyFile = filepath.Join(dir, "agent.crt"), filepath.Join(dir, "agent.key")
	Expect(os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)).To(Succeed())
	Expect(os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}), 0600)).To(Succeed())
	return certFile, keyFile
}

func freePort() int {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	defer lis.Close()
	return lis.Addr().(*net.TCPAddr).Port
}

var _ = Describe("F5 app", func() {
	var (
		device *bigipmock.Mock
		cnf    *config.Config
	)

	BeforeEach(func() {
		device = bigipmock.New()
		DeferCleanup(device.Close)
		dir := GinkgoT().TempDir()
		certFile, keyFile := writeSelfSigned(dir, "f5-test")
		GinkgoT().Setenv("FLOCK_BIGIP_PASSWORD", bigipmock.Password)
		raw := map[string]any{
			"controller": map[string]any{"addr": "127.0.0.1:1", "instance_uuid": "inst-1"},
			"tls":        map[string]any{"cert_file": certFile, "key_file": keyFile, "ca_file": certFile},
			"server":     map[string]any{"listen_host": "127.0.0.1", "listen_port": freePort()},
			"bigip": map[string]any{
				"urls": []string{device.URL()}, "username": bigipmock.Username, "insecure_skip_verify": true,
				"garbage_after_start": "100ms",
			},
		}
		b, err := yaml.Marshal(raw)
		Expect(err).ToNot(HaveOccurred())
		cnf = &config.Config{}
		Expect(yaml.Unmarshal(b, cnf)).To(Succeed())
	})

	It("announces itself as an f5 agent with the F5 extensions", func() {
		a, err := NewApp(cnf, semver.MustParse("1.2.3"))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(a.certman.Stop)

		Expect(a.node.GetClientType()).To(Equal("f5"))
		Expect(a.name).To(Equal("f5-test"))
		categories := map[string][]string{}
		for _, ext := range a.node.GetExtensions() {
			categories[ext.GetTypeUrl()] = append(categories[ext.GetTypeUrl()], ext.GetCategory())
		}
		Expect(categories[helpers.TypeUrl[*f5_backendopts.BackendOptions]()]).To(ConsistOf(
			"flock.api.config.backend.v1.backend.typed_options"))
		Expect(categories[helpers.TypeUrl[*f5_vsopts.VirtualServerOptions]()]).To(ConsistOf(
			"flock.api.config.vs.v1.tcp_route_config.typed_options",
			"flock.api.config.vs.v1.udp_route_config.typed_options",
			"flock.api.config.vs.v1.tls_tcp_routes_config.typed_options",
			"flock.api.config.vs.v1.http_routes_config.typed_options",
		))
	})

	It("refuses to start with a certificate CN breaking the agent name rule", func() {
		certFile, _ := writeSelfSigned(GinkgoT().TempDir(), "F5_Test")
		cnf.TlsConfig.CertFile = certFile

		_, err := NewApp(cnf, semver.MustParse("1.2.3"))

		Expect(err).To(MatchError(ContainSubstring(`"F5_Test"`)))
	})

	It("runs against the device and keeps its tenants while the controller is unreachable", func() {
		device.AddTenant("old", as3.TenantRemark("inst-1"))
		a, err := NewApp(cnf, semver.MustParse("1.2.3"))
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		done := make(chan error, 1)
		go func() { done <- a.Run(ctx) }()

		Consistently(device.Tenants, time.Second).Should(HaveKey("old"))

		cancel()
		Eventually(done, 15*time.Second).Should(Receive(BeNil()))
	})

	It("returns an error when its server can't listen, so the process exits with one", func() {
		taken, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cnf.ServerConfig.ListenPort))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(taken.Close)
		a, err := NewApp(cnf, semver.MustParse("1.2.3"))
		Expect(err).ToNot(HaveOccurred())
		done := make(chan error, 1)
		go func() { done <- a.Run(context.Background()) }()

		Eventually(done, 15*time.Second).Should(Receive(MatchError(errPartFailed)))
	})

	It("stops within the drain grace while a Follow client stays connected", func() {
		setDrainGrace(200 * time.Millisecond)
		a, err := NewApp(cnf, semver.MustParse("1.2.3"))
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		done := make(chan error, 1)
		go func() { done <- a.Run(ctx) }()

		caPEM, err := os.ReadFile(cnf.TlsConfig.CaFile)
		Expect(err).ToNot(HaveOccurred())
		roots := x509.NewCertPool()
		Expect(roots.AppendCertsFromPEM(caPEM)).To(BeTrue())
		clients := helpers.NewGrpcClients(&tls.Config{RootCAs: roots})
		DeferCleanup(clients.Close)
		addr := fmt.Sprintf("127.0.0.1:%d", cnf.ServerConfig.ListenPort)
		logsClient := agentlogsconnect.NewLogsClient(clients.Client(addr))
		followStarted := func() error {
			stream, err := logsClient.Follow(context.Background(), &emptypb.Empty{})
			if err != nil {
				return err
			}
			DeferCleanup(stream.Close)
			_, err = stream.Receive()
			return err
		}
		Eventually(followStarted, 5*time.Second).Should(Succeed())

		cancel()
		Eventually(done, 15*time.Second).Should(Receive(BeNil()))
	})
})
