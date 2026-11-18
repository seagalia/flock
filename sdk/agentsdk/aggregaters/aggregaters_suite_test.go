package aggregaters_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/logging"

	"github.com/seagalia/flock/sdk/flock/services/discovery/v1/svcdiscoveryconnect"
	"github.com/seagalia/flock/sdk/rpcserver"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
)

func TestAggregaters(t *testing.T) {
	RegisterFailHandler(Fail)
	if err := logging.Setup(logging.Options{Console: GinkgoWriter}); err != nil {
		t.Fatal(err)
	}
	RunSpecs(t, "Aggregaters Suite")
}

type adsStream = svcdiscoveryconnect.AggregatedDiscoveryDeltaAggregatedResourcesServerStream

type fakeAdsServer struct {
	handle func(ctx context.Context, stream adsStream) error
	addr   string
}

func startFakeAdsServer(handle func(ctx context.Context, stream adsStream) error) (*fakeAdsServer, *tls.Config) {
	serverTLS, clientTLS := selfSignedTLS()
	server := &fakeAdsServer{handle: handle}
	connectServer := rpcserver.NewServer(func(ctx context.Context, _ string) (context.Context, error) {
		return ctx, nil
	})
	svcdiscoveryconnect.RegisterAggregatedDiscoveryHandler(connectServer, server)
	addr, stop := rpctest.Serve(connectServer, serverTLS)
	server.addr = addr
	DeferCleanup(stop)
	return server, clientTLS
}

func (s *fakeAdsServer) DeltaAggregatedResources(ctx context.Context, stream adsStream) error {
	return s.handle(ctx, stream)
}

func selfSignedTLS() (*tls.Config, *tls.Config) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).ToNot(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake-ads"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).ToNot(HaveOccurred())
	leaf, err := x509.ParseCertificate(der)
	Expect(err).ToNot(HaveOccurred())
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	serverTLS := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}}}
	return serverTLS, &tls.Config{RootCAs: pool}
}
