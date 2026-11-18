//go:build e2e

package env

import (
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
	"sync"
	"time"
)

// AgentServerName is the name a controller expects in the certificate of every agent it calls, whatever its address
const AgentServerName = "flock_agent"

// Certs is a throwaway pki for one e2e run, in pem
type Certs struct {
	CAPem             []byte
	ControllerCertPem []byte
	ControllerKeyPem  []byte
	// ControllerClientCertPem is what a real controller calls the agents with
	ControllerClientCertPem []byte
	ControllerClientKeyPem  []byte

	caCert   *x509.Certificate
	caKey    *ecdsa.PrivateKey
	agentIPs []net.IP
	// agents get their certificate when they start, from the goroutines of the specs
	mutex  sync.Mutex
	serial int64
}

// NewCerts issues the certificates of the controller, AgentCert issues the one of each agent
func NewCerts(controllerServerName string, agentIPs ...string) (*Certs, error) {
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

	// localhost is for the tests dialing a real controller through its mapped port
	c.ControllerCertPem, c.ControllerKeyPem, err = c.issue("controller", []string{controllerServerName, "localhost"},
		[]net.IP{net.ParseIP("127.0.0.1")}, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	if err != nil {
		return nil, fmt.Errorf("controller certificate: %w", err)
	}

	c.ControllerClientCertPem, c.ControllerClientKeyPem, err = c.issue("controller", nil, nil,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	if err != nil {
		return nil, fmt.Errorf("controller client certificate: %w", err)
	}

	c.agentIPs = []net.IP{net.ParseIP("127.0.0.1")}
	for _, ip := range agentIPs {
		c.agentIPs = append(c.agentIPs, net.ParseIP(ip))
	}
	return c, nil
}

// AgentCert is the certificate of the agent named name, its CN: the agent calls the controller with it and serves the
// calls of the controller (AgentServerName) and of the tests (its ips) with it
func (c *Certs) AgentCert(name string) (certPem, keyPem []byte, err error) {
	return c.issue(name, []string{AgentServerName, "localhost"}, c.agentIPs,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth})
}

func (c *Certs) ControllerTLSConfig() *tls.Config {
	cert, err := tls.X509KeyPair(c.ControllerCertPem, c.ControllerKeyPem)
	if err != nil {
		panic(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(c.caCert)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
}

func (c *Certs) LeafCert(cn string, dnsNames ...string) (certPem, keyPem []byte, err error) {
	return c.issue(cn, dnsNames, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
}

func (c *Certs) issue(cn string, dnsNames []string, ips []net.IP, usages []x509.ExtKeyUsage) ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	c.mutex.Lock()
	c.serial++
	serial := c.serial
	c.mutex.Unlock()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     dnsNames,
		IPAddresses:  ips,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usages,
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
