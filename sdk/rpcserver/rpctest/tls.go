package rpctest

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
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/seagalia/flock/sdk/helpers"
)

// NewTLS gives the TLS configurations of a server on 127.0.0.1 and of its client, both signed by the same CA.
// The server verifies client certificates when given, the client has a certificate with clientCommonName as
// common name, or none when it is empty.
func NewTLS(clientCommonName string) (*tls.Config, *tls.Config) {
	caKey, caCert := newCertificate(nil, nil, "rpctest-ca", true)
	pool := x509.NewCertPool()
	pool.AddCert(caCert.Leaf)

	_, serverCert := newCertificate(caKey, caCert.Leaf, "127.0.0.1", false)
	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    pool,
		ClientAuth:   tls.VerifyClientCertIfGiven,
	}
	clientTLS := &tls.Config{RootCAs: pool}
	if clientCommonName != "" {
		_, clientCert := newCertificate(caKey, caCert.Leaf, clientCommonName, false)
		clientTLS.Certificates = []tls.Certificate{clientCert}
	}
	return serverTLS, clientTLS
}

// newCertificate creates a certificate signed by parent, self-signed when parent is nil
func newCertificate(parentKey *ecdsa.PrivateKey, parent *x509.Certificate, commonName string, isCA bool) (*ecdsa.PrivateKey, tls.Certificate) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  isCA,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	if parent == nil {
		parent, parentKey = template, key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, parentKey)
	if err != nil {
		panic(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return key, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// ServerContextOverHTTP makes one gRPC call with the request headers header to a connect server and gives the
// context its handler got. The server and client use serverTLS and clientTLS, both nil for cleartext HTTP/2.
func ServerContextOverHTTP(serverTLS, clientTLS *tls.Config, header map[string]string) (context.Context, error) {
	spec := connect.Spec{Procedure: "/rpctest.Capture/Context", StreamType: connect.StreamTypeUnary}
	handlerCtx := make(chan context.Context, 1)
	server := connect.NewServer()
	server.Register(connect.Method{
		Spec: spec,
		Handler: func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
			handlerCtx <- ctx
			if err := stream.Receive(&emptypb.Empty{}); err != nil {
				return err
			}
			return stream.Send(&emptypb.Empty{})
		},
	})
	addr, stop := Serve(server, serverTLS)
	defer stop()

	clients := helpers.NewGrpcClients(clientTLS)
	defer clients.Close()
	ctx, callInfo := connect.NewClientContext(context.Background())
	for key, value := range header {
		callInfo.RequestHeader().Set(key, value)
	}
	err := clients.Client(addr).CallUnary(ctx, spec, &emptypb.Empty{}, &emptypb.Empty{})
	if err != nil {
		return nil, err
	}
	return <-handlerCtx, nil
}
