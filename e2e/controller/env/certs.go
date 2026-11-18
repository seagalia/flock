//go:build e2e

package env

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
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
)

// agentServerName is the name controller expects from agents, whatever their address
const agentServerName = "flock_agent"

// The controller takes the name of a connecting agent from the CN of its certificate and only accepts a name declared
// for the instance. SubscriberName is the CN of SubscriberTLSConfig, SubscriberInstantiators declares it for the
// instances of the zone named SubscriberZone.
const (
	SubscriberName = "agent-e2e"
	SubscriberUuid = "6f1c2a4e-3b5d-4e8f-9a1b-2c3d4e5f6a7b"
	SubscriberZone = "zone-e2e"
)

// Certs is a throwaway pki for one e2e run, in pem
type Certs struct {
	CAPem                        []byte
	ControllerServerCertPem      []byte
	ControllerServerKeyPem       []byte
	SubscriberClientCertPem      []byte
	SubscriberClientKeyPem       []byte
	AgentServerCertPem           []byte
	AgentServerKeyPem            []byte
	ControllerAgentClientCertPem []byte
	ControllerAgentClientKeyPem  []byte
	JwtPrivateKeyPem             []byte
	JwtPublicKeyPem              []byte

	caCert *x509.Certificate
	caKey  *ecdsa.PrivateKey
	serial int64
}

type leafSpec struct {
	commonName string
	dnsNames   []string
	ips        []net.IP
	usage      x509.ExtKeyUsage
}

// NewCerts issues the controller server certificate for controllerNames, localhost and 127.0.0.1
func NewCerts(controllerNames ...string) (*Certs, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "flock-e2e-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDer, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	caCert, err := x509.ParseCertificate(caDer)
	if err != nil {
		return nil, err
	}
	c := &Certs{
		CAPem:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDer}),
		caCert: caCert,
		caKey:  caKey,
		serial: 1,
	}

	controllerDNSNames := append(append([]string{}, controllerNames...), "localhost")
	c.ControllerServerCertPem, c.ControllerServerKeyPem, err = c.issue(leafSpec{
		commonName: "controller", dnsNames: controllerDNSNames, ips: []net.IP{net.ParseIP("127.0.0.1")}, usage: x509.ExtKeyUsageServerAuth,
	})
	if err != nil {
		return nil, fmt.Errorf("controller server certificate: %w", err)
	}
	c.SubscriberClientCertPem, c.SubscriberClientKeyPem, err = c.issue(leafSpec{
		commonName: SubscriberName, usage: x509.ExtKeyUsageClientAuth,
	})
	if err != nil {
		return nil, fmt.Errorf("agent client certificate: %w", err)
	}
	c.AgentServerCertPem, c.AgentServerKeyPem, err = c.issue(leafSpec{
		commonName: "agent", dnsNames: []string{agentServerName}, usage: x509.ExtKeyUsageServerAuth,
	})
	if err != nil {
		return nil, fmt.Errorf("agent server certificate: %w", err)
	}
	c.ControllerAgentClientCertPem, c.ControllerAgentClientKeyPem, err = c.issue(leafSpec{
		commonName: "controller-agent", usage: x509.ExtKeyUsageClientAuth,
	})
	if err != nil {
		return nil, fmt.Errorf("controller agent client certificate: %w", err)
	}

	jwtKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	jwtPrivateDer, err := x509.MarshalPKCS8PrivateKey(jwtKey)
	if err != nil {
		return nil, err
	}
	jwtPublicDer, err := x509.MarshalPKIXPublicKey(&jwtKey.PublicKey)
	if err != nil {
		return nil, err
	}
	c.JwtPrivateKeyPem = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: jwtPrivateDer})
	c.JwtPublicKeyPem = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: jwtPublicDer})
	return c, nil
}

// SubscriberTLSConfig is what an agent dials controller with
func (c *Certs) SubscriberTLSConfig(serverName string) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{c.keyPair(c.SubscriberClientCertPem, c.SubscriberClientKeyPem)},
		RootCAs:      c.caPool(),
		ServerName:   serverName,
	}
}

// AgentClientTLSConfig is what an agent named name dials controller with, the CN of its certificate
func (c *Certs) AgentClientTLSConfig(name, serverName string) (*tls.Config, error) {
	certPem, keyPem, err := c.issue(leafSpec{commonName: name, usage: x509.ExtKeyUsageClientAuth})
	if err != nil {
		return nil, fmt.Errorf("client certificate of agent %s: %w", name, err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{c.keyPair(certPem, keyPem)},
		RootCAs:      c.caPool(),
		ServerName:   serverName,
	}, nil
}

// AgentTLSConfig is what an agent listens with, it only accepts clients signed by the CA
func (c *Certs) AgentTLSConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{c.keyPair(c.AgentServerCertPem, c.AgentServerKeyPem)},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    c.caPool(),
	}
}

// UserTLSConfig is what an end user dials controller with, users authenticate by jwt and not by certificate
func (c *Certs) UserTLSConfig(serverName string) *tls.Config {
	return &tls.Config{RootCAs: c.caPool(), ServerName: serverName}
}

// ControllerAgentTLSConfig is what controller dials agents with
func (c *Certs) ControllerAgentTLSConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{c.keyPair(c.ControllerAgentClientCertPem, c.ControllerAgentClientKeyPem)},
		RootCAs:      c.caPool(),
		ServerName:   agentServerName,
	}
}

// WriteFiles writes the fixed file names controller's rendered config points at
func (c *Certs) WriteFiles(dir string) error {
	files := map[string][]byte{
		"ca.pem":                   c.CAPem,
		"controller.pem":           c.ControllerServerCertPem,
		"controller-key.pem":       c.ControllerServerKeyPem,
		"controller-agent.pem":     c.ControllerAgentClientCertPem,
		"controller-agent-key.pem": c.ControllerAgentClientKeyPem,
		"jwt.pem":                  c.JwtPrivateKeyPem,
		"jwt.pub":                  c.JwtPublicKeyPem,
	}
	for name, content := range files {
		// keys must be 0600 and certs share the mode for simplicity
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
	}
	return nil
}

func (c *Certs) caPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(c.caCert)
	return pool
}

func (c *Certs) keyPair(certPem, keyPem []byte) tls.Certificate {
	pair, err := tls.X509KeyPair(certPem, keyPem)
	if err != nil {
		panic(err)
	}
	return pair
}

func (c *Certs) issue(spec leafSpec) (certPem, keyPem []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	c.serial++
	template := &x509.Certificate{
		SerialNumber: big.NewInt(c.serial),
		Subject:      pkix.Name{CommonName: spec.commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     spec.dnsNames,
		IPAddresses:  spec.ips,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{spec.usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, c.caCert, &key.PublicKey, c.caKey)
	if err != nil {
		return nil, nil, err
	}
	keyDer, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}), nil
}
