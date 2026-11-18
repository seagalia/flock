//go:build e2e

package f5_test

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
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/f5/bigip/bigipmock"
	"github.com/seagalia/flock/sdk/agentsdk/fakecontroller"
	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1/agentstatusconnect"
	"github.com/seagalia/flock/sdk/helpers"
)

const instanceUUID = "f5-e2e"

var (
	agentBin   string
	certFile   string
	keyFile    string
	certPool   *x509.CertPool
	controller *fakecontroller.Server
	ctrlAddr   string
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(20 * time.Second)
	SetDefaultEventuallyPollingInterval(200 * time.Millisecond)
	RunSpecs(t, "flock F5 agent e2e Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	dir := GinkgoT().TempDir()
	agentBin = filepath.Join(dir, "flock")
	build := exec.CommandContext(ctx, "go", "build", "-o", agentBin, ".")
	build.Dir = filepath.Join("..", "..", "..")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := build.CombinedOutput()
	Expect(err).ToNot(HaveOccurred(), "build flock: %s", out)

	var serverTLS *tls.Config
	serverTLS, certPool = writeSelfSigned(dir)
	serverTLS.ClientAuth = tls.RequireAndVerifyClientCert
	serverTLS.ClientCAs = certPool
	controller = fakecontroller.New(serverTLS)
	// the agent connects with the certificate of writeSelfSigned, its CN names it
	controller.DeclareAgent("f5-e2e", "f5-e2e-agent-uuid")
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	ctrlAddr = lis.Addr().String()
	go func() { _ = controller.Serve(lis) }()
	DeferCleanup(controller.Stop)
}, NodeTimeout(5*time.Minute))

// writeSelfSigned writes one certificate for 127.0.0.1, its own CA: the fake controller serves it, the agent uses it
// as its server and client certificate and trusts it.
func writeSelfSigned(dir string) (*tls.Config, *x509.CertPool) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).ToNot(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "f5-e2e"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).ToNot(HaveOccurred())
	keyDer, err := x509.MarshalECPrivateKey(key)
	Expect(err).ToNot(HaveOccurred())
	certFile, keyFile = filepath.Join(dir, "agent.crt"), filepath.Join(dir, "agent.key")
	Expect(os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)).To(Succeed())
	Expect(os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}), 0600)).To(Succeed())
	leaf, err := x509.ParseCertificate(der)
	Expect(err).ToNot(HaveOccurred())
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}}}, pool
}

type agentProcess struct {
	cmd        *exec.Cmd
	configPath string
	port       int
}

// startAgent runs the agent against the devices, in this order. The timings are short so scenarios run fast,
// max_incomplete stays long so a missing reference is visible.
func startAgent(devices ...*bigipmock.Mock) *agentProcess {
	GinkgoHelper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	port := lis.Addr().(*net.TCPAddr).Port
	Expect(lis.Close()).To(Succeed())

	urls := ""
	for _, d := range devices {
		urls += fmt.Sprintf("%q,", d.URL())
	}
	config := fmt.Sprintf(`controller:
  addr: %s
  instance_uuid: %s
tls:
  cert_file: %s
  key_file: %s
  ca_file: %s
server:
  listen_host: 127.0.0.1
  listen_port: %d
log:
  level: debug
bigip:
  urls: [%s]
  username: %s
  insecure_skip_verify: true
  debounce: 200ms
  max_debounce: 1s
  max_incomplete: 1m
  resync_interval: 1h
  garbage_after_start: 3s
  request_timeout: 20s
`, ctrlAddr, instanceUUID, certFile, keyFile, certFile, port, urls[:len(urls)-1], bigipmock.Username)
	configPath := filepath.Join(GinkgoT().TempDir(), "f5.yml")
	Expect(os.WriteFile(configPath, []byte(config), 0600)).To(Succeed())

	p := &agentProcess{configPath: configPath, port: port}
	p.start()
	DeferCleanup(p.stop)
	return p
}

func (p *agentProcess) start() {
	GinkgoHelper()
	p.cmd = exec.Command(agentBin, "agent", "f5", "start", "-c", p.configPath)
	p.cmd.Env = append(os.Environ(), "FLOCK_BIGIP_PASSWORD="+bigipmock.Password)
	p.cmd.Stdout = GinkgoWriter
	p.cmd.Stderr = GinkgoWriter
	Expect(p.cmd.Start()).To(Succeed())
	Eventually(func() bool {
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", p.port))
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}).Should(BeTrue())
}

func (p *agentProcess) stop() {
	GinkgoHelper()
	if p.cmd == nil || p.cmd.Process == nil {
		return
	}
	cmd := p.cmd
	p.cmd = nil
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		Expect(err).ToNot(HaveOccurred(), "the agent exits cleanly on SIGTERM")
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		Fail(fmt.Sprintf("the agent did not stop within 30s of SIGTERM, killed: %v", <-done))
	}
}

func (p *agentProcess) restart() {
	p.stop()
	p.start()
}

func (p *agentProcess) statusClients() (agentstatusconnect.ResourceStatusClient, agentstatusconnect.AgentStatusClient) {
	clients := helpers.NewGrpcClients(&tls.Config{RootCAs: certPool})
	DeferCleanup(clients.Close)
	conn := clients.Client(fmt.Sprintf("127.0.0.1:%d", p.port))
	return agentstatusconnect.NewResourceStatusClient(conn), agentstatusconnect.NewAgentStatusClient(conn)
}

func withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
