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
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/Masterminds/semver/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/provisioners/acme/challenges"
	"github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
	"github.com/seagalia/flock/sdk/storers"
	edsv1 "github.com/seagalia/vigie/sdk/vigie/eds/v1"
	"github.com/seagalia/vigie/sdk/vigie/eds/v1/edsconnect"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

func freePort() uint16 {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	defer lis.Close()
	return uint16(lis.Addr().(*net.TCPAddr).Port)
}

// writeClientCert writes a self-signed certificate and its key, enough for the tls config of the app.
func writeClientCert() (certFile, keyFile string) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "provisioner"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		// its own CA and valid for loopback, so a spec can call the server of the app with it
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())
	keyDer, err := x509.MarshalECPrivateKey(key)
	Expect(err).NotTo(HaveOccurred())

	dir := GinkgoT().TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	Expect(os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)).To(Succeed())
	Expect(os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}), 0600)).To(Succeed())
	return certFile, keyFile
}

var _ = Describe("NewApp", func() {
	var cnf *config.Config

	BeforeEach(func() {
		certFile, keyFile := writeClientCert()
		cnf = &config.Config{
			ControllerConfig: &agentconfig.ControllerConfig{Addr: "127.0.0.1:1", InstanceUuid: "instance-1"},
			TlsConfig:        &agentconfig.TlsConfig{CertFile: certFile, KeyFile: keyFile},
			ServerConfig:     &agentconfig.ServerConfig{ListenHost: "127.0.0.1", ListenPort: 18002},
		}
	})

	newApp := func() *App {
		app, err := NewApp(cnf, semver.MustParse("1.2.3"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(app.certman.Stop)
		return app
	}

	It("subscribes to nothing when acme is disabled", func() {
		app := newApp()

		Expect(app.certManager).To(BeNil())
		Expect(app.rt.Handlers("")).To(BeEmpty())
	})

	It("subscribes to the certificates and their secrets when acme is enabled", func() {
		cnf.Acme = config.AcmeConfig{Enabled: true, DefaultCADirUrl: "https://ca.example.com/dir"}

		app := newApp()

		Expect(app.certManager).NotTo(BeNil())
		Expect(app.rt.Handlers("")).To(HaveLen(2))
	})

	It("leaves the number_resources metrics to the command running it", func() {
		agentStore := storers.NewMemoryManager()
		stores.CountResourcesOf(agentStore)
		DeferCleanup(func() { stores.CountResourcesOf(nil) })

		app := newApp()
		Expect(app.Runtime().Store.Certificates().Create(context.Background(), &cert.Certificate{UUID: "cert-1"})).To(Succeed())

		Expect(app.Runtime()).To(BeIdenticalTo(app.rt))
		Expect(stores.LenStore[*cert.Certificate]()).To(BeZero())
	})

	It("announces itself as a provisioner", func() {
		app := newApp()

		Expect(app.node.GetClientType()).To(Equal("provisioner"))
		Expect(app.node.GetClientVersion().GetMajor()).To(BeEquivalentTo(1))
	})

	It("takes its name from the common name of its certificate", func() {
		app := newApp()

		Expect(app.name).To(Equal("provisioner"))
	})
})

var _ = Describe("Run", func() {
	It("stops within the drain grace while a Follow client stays connected", func() {
		setDrainGrace(200 * time.Millisecond)
		certFile, keyFile := writeClientCert()
		cnf := &config.Config{
			ControllerConfig: &agentconfig.ControllerConfig{Addr: "127.0.0.1:1", InstanceUuid: "instance-1"},
			TlsConfig:        &agentconfig.TlsConfig{CertFile: certFile, KeyFile: keyFile, CaFile: certFile},
			ServerConfig:     &agentconfig.ServerConfig{ListenHost: "127.0.0.1", ListenPort: freePort()},
		}
		app, err := NewApp(cnf, semver.MustParse("1.2.3"))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		done := make(chan error, 1)
		go func() { done <- app.Run(ctx) }()

		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		Expect(err).NotTo(HaveOccurred())
		clients := helpers.NewGrpcClients(&tls.Config{RootCAs: app.tlsConf.RootCAs, Certificates: []tls.Certificate{pair}})
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

var _ = Describe("docker", func() {
	var cnf *config.Config

	BeforeEach(func() {
		certFile, keyFile := writeClientCert()
		cnf = &config.Config{
			ControllerConfig: &agentconfig.ControllerConfig{Addr: "127.0.0.1:1", InstanceUuid: "instance-1"},
			TlsConfig:        &agentconfig.TlsConfig{CertFile: certFile, KeyFile: keyFile, CaFile: certFile},
			ServerConfig:     &agentconfig.ServerConfig{ListenHost: "127.0.0.1", ListenPort: freePort()},
		}
	})

	It("builds no docker manager when the docker block is disabled", func() {
		app, err := NewApp(cnf, semver.MustParse("1.2.3"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(app.certman.Stop)

		Expect(app.dockerManager).To(BeNil())
	})

	It("provisions under the CN of its certificate when no ads handler opens a stream", func() {
		cnf.Docker = config.DockerConfig{Enabled: true, Host: "unix:///nonexistent/docker.sock", Vip: "local"}
		app, err := NewApp(cnf, semver.MustParse("1.2.3"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(app.certman.Stop)

		Expect(app.rt.Handlers("")).To(BeEmpty())
		Expect(app.provisionIdentity().WaitAgentUuid(context.Background())).To(Equal("provisioner"))
	})

	It("provisions under the uuid the controller gives when acme opens a stream", func() {
		cnf.Docker = config.DockerConfig{Enabled: true, Host: "unix:///nonexistent/docker.sock", Vip: "local"}
		cnf.Acme = config.AcmeConfig{Enabled: true, DefaultCADirUrl: "https://ca.example.com/dir", LeaseTTL: time.Minute}
		app, err := NewApp(cnf, semver.MustParse("1.2.3"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(app.certman.Stop)

		Expect(app.provisionIdentity()).To(BeIdenticalTo(app.aggregator))
	})

	It("serves the eds to a client with no certificate", func() {
		edsPort := freePort()
		cnf.Docker = config.DockerConfig{
			Enabled: true,
			// the manager retries it until the spec ends, the eds serves meanwhile
			Host: "unix:///nonexistent/docker.sock",
			Vip:  "local",
			Eds: config.EdsConfig{
				ListenHost:   "127.0.0.1",
				ListenPort:   edsPort,
				AdvertiseUrl: fmt.Sprintf("grpcs://127.0.0.1:%d", edsPort),
			},
		}
		app, err := NewApp(cnf, semver.MustParse("1.2.3"))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		done := make(chan error, 1)
		go func() { done <- app.Run(ctx) }()

		clients := helpers.NewGrpcClients(&tls.Config{RootCAs: app.tlsConf.RootCAs})
		DeferCleanup(clients.Close)
		edsClient := edsconnect.NewEndpointDiscoveryServiceClient(clients.Client(fmt.Sprintf("127.0.0.1:%d", edsPort)))
		getEndpoints := func() connect.Code {
			_, err := edsClient.GetEndpoints(context.Background(), &edsv1.GetEndpointsRequest{ServiceName: "shop-web"})
			return connect.CodeOf(err)
		}
		Eventually(getEndpoints, 5*time.Second).Should(Equal(connect.CodeNotFound))

		cancel()
		Eventually(done, 15*time.Second).Should(Receive(BeNil()))
	})
})

var _ = Describe("simpleAuth", func() {
	It("refuses a call without a client certificate, even from localhost", func() {
		ctx := rpctest.ServerContextWithInfo(context.Background(), &connect.CallInfo{PeerAddr: "127.0.0.1:4242"})

		_, err := simpleAuth(ctx, "")

		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated))
	})
})

var _ = Describe("shared challenges", func() {
	var cnf *config.Config

	BeforeEach(func() {
		certFile, keyFile := writeClientCert()
		cnf = &config.Config{
			ControllerConfig: &agentconfig.ControllerConfig{Addr: "127.0.0.1:1", InstanceUuid: "instance-1"},
			TlsConfig:        &agentconfig.TlsConfig{CertFile: certFile, KeyFile: keyFile, CaFile: certFile},
			ServerConfig:     &agentconfig.ServerConfig{ListenHost: "127.0.0.1", ListenPort: freePort()},
			Acme: config.AcmeConfig{
				Enabled:         true,
				DefaultCADirUrl: "https://ca.example.com/dir",
				LeaseTTL:        time.Minute,
			},
		}
	})

	feedSecret := func(app *App, aSecret *secret.Secret) {
		var secretHandler *adshandlers.AdsHandler[*secret.Secret]
		for _, handler := range app.rt.Handlers("") {
			if h, isSecretHandler := handler.(*adshandlers.AdsHandler[*secret.Secret]); isSecretHandler {
				secretHandler = h
			}
		}
		Expect(secretHandler).NotTo(BeNil())
		packed, err := anypb.New(aSecret)
		Expect(err).NotTo(HaveOccurred())
		resource := &svcdiscovery.Resource{Uuid: aSecret.UUID, Signature: "v1", Resource: packed}
		Expect(secretHandler.AddResource(context.Background(), resource)).To(Succeed())
	}

	It("answers the http-01 challenge of a secret received through ADS", func() {
		httpPort := freePort()
		cnf.Acme.Challenges = config.Challenges{Http01Enabled: true, Host: "127.0.0.1", Http01Port: uint(httpPort)}
		app, err := NewApp(cnf, semver.MustParse("1.2.3"))
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- app.Run(ctx) }()
		DeferCleanup(func() {
			cancel()
			Eventually(done, 15*time.Second).Should(Receive())
		})

		feedSecret(app, &secret.Secret{
			UUID: "secret-1",
			Metadata: map[string]string{
				challenges.MetadataKey:   "true",
				helpers.MetadataLockKey:  "true",
				"flock.provisioner.acme": "true",
			},
			Data: challenges.EncodeData([]challenges.Challenge{
				{Domain: "example.com", Token: "tok-1", KeyAuth: "tok-1.thumbprint"},
			}),
		})

		url := fmt.Sprintf("http://127.0.0.1:%d/.well-known/acme-challenge/tok-1", httpPort)
		body := func() (string, error) {
			resp, err := http.Get(url)
			if err != nil {
				return "", err
			}
			defer resp.Body.Close()
			content, err := io.ReadAll(resp.Body)
			return string(content), err
		}
		Eventually(body, 5*time.Second).Should(Equal("tok-1.thumbprint"))
	})

	It("listens on no challenge port when both challenges are disabled", func() {
		httpPort := freePort()
		tlsPort := freePort()
		cnf.Acme.Challenges = config.Challenges{Host: "127.0.0.1", Http01Port: uint(httpPort), TlsAlpn01Port: uint(tlsPort)}
		app, err := NewApp(cnf, semver.MustParse("1.2.3"))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(app.certman.Stop)

		Expect(app.challengeTable).To(BeNil())
		for _, port := range []uint16{httpPort, tlsPort} {
			lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			Expect(err).NotTo(HaveOccurred())
			Expect(lis.Close()).To(Succeed())
		}
	})

	It("fails and frees the http-01 port when the tls-alpn-01 port is taken", func() {
		taken, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(taken.Close)
		httpPort := freePort()
		cnf.Acme.Challenges = config.Challenges{
			Http01Enabled: true, TlsAlpn01Enabled: true, Host: "127.0.0.1",
			Http01Port: uint(httpPort), TlsAlpn01Port: uint(taken.Addr().(*net.TCPAddr).Port),
		}

		_, err = NewApp(cnf, semver.MustParse("1.2.3"))

		Expect(err).To(HaveOccurred())
		lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", httpPort))
		Expect(err).NotTo(HaveOccurred())
		Expect(lis.Close()).To(Succeed())
	})
})
