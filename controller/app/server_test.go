package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ArthurHlt/emitter"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	"github.com/seagalia/flock/sdk/observe"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
)

func writePem(path, blockType string, der []byte) {
	err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), 0o600)
	Expect(err).ToNot(HaveOccurred())
}

func ecKeyDer(key any) []byte {
	der, err := x509.MarshalECPrivateKey(key.(*ecdsa.PrivateKey))
	Expect(err).ToNot(HaveOccurred())
	return der
}

func freeAddr() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())
	defer listener.Close() // nolint:errcheck
	return listener.Addr().String()
}

// newTestApp gives an app on a sqlite store in a temporary directory, serving on listenAddr and, when not empty, on
// the plain http loopbackAddr
func newTestApp(listenAddr, loopbackAddr string) *App {
	// each app registers its collectors by name, a registry per spec lets several live in the process
	previous := prometheus.DefaultRegisterer
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	DeferCleanup(func() { prometheus.DefaultRegisterer = previous })

	dir := GinkgoT().TempDir()
	serverTLS, _ := rpctest.NewTLS("")
	cert := serverTLS.Certificates[0]
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	writePem(certFile, "CERTIFICATE", cert.Certificate[0])
	writePem(keyFile, "EC PRIVATE KEY", ecKeyDer(cert.PrivateKey))

	jwtKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).ToNot(HaveOccurred())
	jwtPem := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecKeyDer(jwtKey)})
	indentedJwtPem := strings.ReplaceAll(strings.TrimSpace(string(jwtPem)), "\n", "\n      ")

	configYaml := fmt.Sprintf(`
log:
  level: error
server:
  listen_addr: %q
  loopback_http_addr: %q
  jwt_maker:
    alg: ES256
    private_key: |
      %s
  login:
    type: basic
    basic_auth_options:
      - username: admin
        password: admin
  tls:
    cert_file: %q
    key_file: %q
mtls:
  cert_file: %q
  key_file: %q
store:
  sql:
    type: sqlite
    dsn: %q
    secret_key: test
`, listenAddr, loopbackAddr, indentedJwtPem, certFile, keyFile, certFile, keyFile, filepath.Join(dir, "store.db"))
	configPath := filepath.Join(dir, "config.yml")
	Expect(os.WriteFile(configPath, []byte(configYaml), 0o600)).To(Succeed())
	cnf, err := config.ReadConfig(configPath)
	Expect(err).ToNot(HaveOccurred())

	app, err := NewApp(cnf)
	Expect(err).ToNot(HaveOccurred())
	return app
}

// runApp runs app until the spec cancels it, it gives the cancel and the result of RunServer
func runApp(app *App) (context.CancelFunc, <-chan error) {
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	runDone := make(chan error, 1)
	go func() { runDone <- app.RunServer(ctx) }()
	return cancel, runDone
}

func waitServing(loopbackAddr string) {
	Eventually(func() (int, error) {
		resp, err := http.Get("http://" + loopbackAddr + "/healthz")
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close() // nolint:errcheck
		return resp.StatusCode, nil
	}).WithTimeout(10 * time.Second).Should(Equal(http.StatusOK))
}

// fakeBridge stands for the nats bridge, it records the events it would publish to the other controllers while it runs
type fakeBridge struct {
	mutex       sync.Mutex
	identifiers []string
}

func (b *fakeBridge) Run(ctx context.Context) error {
	observe.OnAll(b)
	<-ctx.Done()
	observe.OffAll(b)
	return nil
}

func (b *fakeBridge) Observe(e emitter.Event) {
	typedEvent, ok := e.(*emitter.EventOf[*observer.Subject])
	if !ok {
		return
	}
	b.mutex.Lock()
	defer b.mutex.Unlock()
	b.identifiers = append(b.identifiers, typedEvent.TypedSubject().GetIdentifier())
}

func (b *fakeBridge) published() []string {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	return slices.Clone(b.identifiers)
}

var _ = Describe("RunServer", func() {
	It("returns the error of a listener that can't bind", func() {
		taken, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(taken.Close)
		app := newTestApp(taken.Addr().String(), "")

		_, runDone := runApp(app)

		Eventually(runDone).WithTimeout(5 * time.Second).Should(Receive(MatchError(ContainSubstring("address already in use"))))
	})

	It("releases the database and the app context when it fails before serving", func() {
		app := newTestApp(freeAddr(), "")
		app.cnf.Server.Tls.CertFile = filepath.Join(GinkgoT().TempDir(), "missing.pem")

		_, runDone := runApp(app)

		Eventually(runDone).WithTimeout(5 * time.Second).Should(Receive(HaveOccurred()))
		Expect(app.ctx.Err()).To(HaveOccurred())
		Expect(app.dbMan.Ping(context.Background())).To(HaveOccurred())
	})

	It("keeps the database and the nats bridge for a call in flight until the servers have stopped", func() {
		loopbackAddr := freeAddr()
		app := newTestApp(freeAddr(), loopbackAddr)
		bridge := &fakeBridge{}
		app.multiRunnable = bridge
		entered := make(chan struct{})
		release := make(chan struct{})
		storeErr := make(chan error, 1)
		app.testRoutes = func(mux *http.ServeMux) {
			mux.HandleFunc("/test/blocked", func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				err := app.storeManager.VIPs().Create(context.Background(), &vip.VIP{UUID: "vip-in-flight", Name: "vip-in-flight"})
				<-observe.EmitSubject(observer.EventType_CREATE_SUBJECT, &observer.Subject{Identifier: "vip-in-flight"})
				storeErr <- err
			})
		}
		cancel, runDone := runApp(app)
		waitServing(loopbackAddr)
		go func() {
			resp, err := http.Get("http://" + loopbackAddr + "/test/blocked")
			if err == nil {
				resp.Body.Close() // nolint:errcheck
			}
		}()
		Eventually(entered).Should(BeClosed())

		cancel()
		Consistently(runDone).WithTimeout(2 * time.Second).ShouldNot(Receive())
		close(release)

		Eventually(storeErr).Should(Receive(BeNil()))
		Eventually(runDone).WithTimeout(5 * time.Second).Should(Receive(BeNil()))
		Expect(bridge.published()).To(ContainElement("vip-in-flight"))
	})

	It("waits for a handler whose client reset its stream before closing the database", func() {
		listenAddr := freeAddr()
		loopbackAddr := freeAddr()
		app := newTestApp(listenAddr, loopbackAddr)
		entered := make(chan struct{})
		release := make(chan struct{})
		storeErr := make(chan error, 1)
		app.testRoutes = func(mux *http.ServeMux) {
			mux.HandleFunc("/test/reset", func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				storeErr <- app.storeManager.VIPs().Create(context.Background(), &vip.VIP{UUID: "vip-after-reset", Name: "vip-after-reset"})
			})
		}
		cancel, runDone := runApp(app)
		waitServing(loopbackAddr)
		client := &http.Client{Transport: &http.Transport{
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // nolint:gosec self-signed test certificate
			ForceAttemptHTTP2: true,
		}}
		DeferCleanup(client.CloseIdleConnections)
		reqCtx, resetStream := context.WithCancel(context.Background())
		DeferCleanup(resetStream)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "https://"+listenAddr+"/test/reset", nil)
		Expect(err).ToNot(HaveOccurred())
		go func() {
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close() // nolint:errcheck
			}
		}()
		Eventually(entered).Should(BeClosed())
		resetStream()

		cancel()
		Consistently(runDone).WithTimeout(2 * time.Second).ShouldNot(Receive())
		close(release)

		Eventually(storeErr).Should(Receive(BeNil()))
		Eventually(runDone).WithTimeout(5 * time.Second).Should(Receive(BeNil()))
	})

	It("ends a stream that waits on its request when the grace has passed", func() {
		loopbackAddr := freeAddr()
		app := newTestApp(freeAddr(), loopbackAddr)
		app.testRoutes = func(mux *http.ServeMux) {
			mux.HandleFunc("/test/stream", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			})
		}
		cancel, runDone := runApp(app)
		waitServing(loopbackAddr)
		resp, err := http.Get("http://" + loopbackAddr + "/test/stream")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(resp.Body.Close)

		cancel()

		Eventually(runDone).WithTimeout(15 * time.Second).Should(Receive(BeNil()))
	})
})

var _ = Describe("loadMTlsConfig", func() {
	It("refuses a ca file holding no certificate", func() {
		dir := GinkgoT().TempDir()
		serverTLS, _ := rpctest.NewTLS("")
		cert := serverTLS.Certificates[0]
		certFile := filepath.Join(dir, "cert.pem")
		keyFile := filepath.Join(dir, "key.pem")
		writePem(certFile, "CERTIFICATE", cert.Certificate[0])
		writePem(keyFile, "EC PRIVATE KEY", ecKeyDer(cert.PrivateKey))
		caFile := filepath.Join(dir, "ca.pem")
		Expect(os.WriteFile(caFile, []byte("not a certificate"), 0o600)).To(Succeed())
		app := &App{cnf: &config.Config{Mtls: &config.Mtls{CertFile: certFile, KeyFile: keyFile, CaFile: caFile}}}

		Expect(app.loadMTlsConfig()).To(MatchError(ContainSubstring("no certificate found in " + caFile)))
	})
})
