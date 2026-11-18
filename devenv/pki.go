package devenv

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// agentServerName is the name controller expects in the agent certificate (mtls.server_name default)
const agentServerName = "flock_agent"

const pkiValidity = 365 * 24 * time.Hour

const caRenewBefore = 30 * 24 * time.Hour

// pki is the certificates and jwt keys of a dev session, the paths of its pem files once written
type pki struct {
	CaFile    string
	CaKeyFile string

	ControllerCertFile string
	ControllerKeyFile  string
	// ControllerAgentCertFile is the client certificate controller dials agents with
	ControllerAgentCertFile string
	ControllerAgentKeyFile  string
	// AgentCertFile serves the agent and authenticates it on controller, its CN is the name of the declared agent
	AgentCertFile string
	AgentKeyFile  string
	// ProvisionerCertFile is the same for the provisioner
	ProvisionerCertFile string
	ProvisionerKeyFile  string

	JwtPrivateKeyFile string
	JwtPublicKeyFile  string
	JwtPrivateKeyPem  string
	JwtPublicKeyPem   string
}

type leafSpec struct {
	certFile    string
	keyFile     string
	commonName  string
	dnsNames    []string
	ipAddresses []net.IP
	usages      []x509.ExtKeyUsage
}

// ensurePki completes the PKI of dir and keeps what is already there, so a cli or a browser that trusts the CA keeps
// working across starts. Everything is issued again when the CA is missing or close to its expiry, only the controller
// certificate is when its SANs miss one of controllerNames (DNS names or IPs, localhost and 127.0.0.1 are always
// added).
func ensurePki(dir string, controllerNames []string) (*pki, error) {
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return nil, err
	}
	inDir := func(name string) string { return filepath.Join(dir, name) }
	p := &pki{
		CaFile:                  inDir("ca.pem"),
		CaKeyFile:               inDir("ca-key.pem"),
		ControllerCertFile:      inDir("controller.pem"),
		ControllerKeyFile:       inDir("controller-key.pem"),
		ControllerAgentCertFile: inDir("controller-agent.pem"),
		ControllerAgentKeyFile:  inDir("controller-agent-key.pem"),
		AgentCertFile:           inDir("agent.pem"),
		AgentKeyFile:            inDir("agent-key.pem"),
		ProvisionerCertFile:     inDir("provisioner.pem"),
		ProvisionerKeyFile:      inDir("provisioner-key.pem"),
		JwtPrivateKeyFile:       inDir("jwt-private.pem"),
		JwtPublicKeyFile:        inDir("jwt-public.pem"),
	}

	caCert, caKey, err := loadCa(dir, p)
	if err != nil {
		return nil, err
	}
	reissueAll := caCert == nil
	if reissueAll {
		caCert, caKey, err = newCa(p)
		if err != nil {
			return nil, err
		}
	}

	controllerNames = slices.DeleteFunc(slices.Clone(controllerNames), func(name string) bool { return name == "" })
	dnsNames, ipAddresses := splitNames(controllerNames)
	// agents serve the controller and authenticate on it with one certificate
	agentUsages := []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}
	agentDnsNames := []string{agentServerName, "localhost"}
	leaves := []leafSpec{
		{certFile: p.ControllerCertFile, keyFile: p.ControllerKeyFile, commonName: "controller", dnsNames: dnsNames, ipAddresses: ipAddresses, usages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}},
		{certFile: p.ControllerAgentCertFile, keyFile: p.ControllerAgentKeyFile, commonName: "controller-agent", usages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		{certFile: p.AgentCertFile, keyFile: p.AgentKeyFile, commonName: agentName, dnsNames: agentDnsNames, usages: agentUsages},
		{certFile: p.ProvisionerCertFile, keyFile: p.ProvisionerKeyFile, commonName: provisionerName, dnsNames: agentDnsNames, usages: agentUsages},
	}
	for _, leaf := range leaves {
		keep, err := keepLeaf(leaf, controllerNames, reissueAll)
		if err != nil {
			return nil, err
		}
		if keep {
			continue
		}
		err = issueLeaf(caCert, caKey, leaf)
		if err != nil {
			return nil, fmt.Errorf("certificate %s: %w", leaf.commonName, err)
		}
	}

	err = ensureJwtKeys(p)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// loadCa returns a nil certificate when the CA has to be generated: a file missing, or the CA close to its expiry
func loadCa(dir string, p *pki) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPem, certErr := os.ReadFile(p.CaFile)
	keyPem, keyErr := os.ReadFile(p.CaKeyFile)
	if errors.Is(certErr, fs.ErrNotExist) || errors.Is(keyErr, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if certErr != nil {
		return nil, nil, readError(p.CaFile, certErr, dir)
	}
	if keyErr != nil {
		return nil, nil, readError(p.CaKeyFile, keyErr, dir)
	}
	caCert, err := parseCertPem(certPem)
	if err != nil {
		return nil, nil, readError(p.CaFile, err, dir)
	}
	caKey, err := parseKeyPem(keyPem)
	if err != nil {
		return nil, nil, readError(p.CaKeyFile, err, dir)
	}
	if time.Until(caCert.NotAfter) < caRenewBefore {
		return nil, nil, nil
	}
	return caCert, caKey, nil
}

func newCa(p *pki) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "flock-dev-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(pkiValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDer, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	caCert, err := x509.ParseCertificate(caDer)
	if err != nil {
		return nil, nil, err
	}
	keyDer, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		return nil, nil, err
	}
	err = os.WriteFile(p.CaKeyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}), 0600)
	if err != nil {
		return nil, nil, err
	}
	err = os.WriteFile(p.CaFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDer}), 0600)
	if err != nil {
		return nil, nil, err
	}
	return caCert, caKey, nil
}

// keepLeaf tells whether the leaf on disk stays: it exists, parses, and for the controller one covers every name asked
func keepLeaf(leaf leafSpec, controllerNames []string, reissue bool) (bool, error) {
	if reissue {
		return false, nil
	}
	dir := filepath.Dir(leaf.certFile)
	certPem, certErr := os.ReadFile(leaf.certFile)
	_, keyErr := os.Stat(leaf.keyFile)
	if errors.Is(certErr, fs.ErrNotExist) || errors.Is(keyErr, fs.ErrNotExist) {
		return false, nil
	}
	if certErr != nil {
		return false, readError(leaf.certFile, certErr, dir)
	}
	if keyErr != nil {
		return false, readError(leaf.keyFile, keyErr, dir)
	}
	_, err := tls.LoadX509KeyPair(leaf.certFile, leaf.keyFile)
	if err != nil {
		return false, readError(leaf.certFile, err, dir)
	}
	if leaf.commonName != "controller" {
		return true, nil
	}
	cert, err := parseCertPem(certPem)
	if err != nil {
		return false, readError(leaf.certFile, err, dir)
	}
	for _, name := range controllerNames {
		if cert.VerifyHostname(name) != nil {
			return false, nil
		}
	}
	return true, nil
}

func splitNames(controllerNames []string) ([]string, []net.IP) {
	names := append([]string{"localhost", "127.0.0.1"}, controllerNames...)
	var dnsNames []string
	var ipAddresses []net.IP
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		ip := net.ParseIP(name)
		if ip == nil {
			dnsNames = append(dnsNames, name)
			continue
		}
		ipAddresses = append(ipAddresses, ip)
	}
	return dnsNames, ipAddresses
}

func newSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

func readError(path string, err error, dir string) error {
	return fmt.Errorf("read %s: %w: remove %s to start over", path, err, dir)
}

func parseCertPem(content []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(content)
	if block == nil {
		return nil, errors.New("no pem block")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseKeyPem(content []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(content)
	if block == nil {
		return nil, errors.New("no pem block")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

func issueLeaf(caCert *x509.Certificate, caKey *ecdsa.PrivateKey, leaf leafSpec) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := newSerial()
	if err != nil {
		return err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: leaf.commonName},
		DNSNames:     leaf.dnsNames,
		IPAddresses:  append([]net.IP{net.ParseIP("127.0.0.1")}, leaf.ipAddresses...),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(pkiValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  leaf.usages,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDer, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	err = os.WriteFile(leaf.keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}), 0600)
	if err != nil {
		return err
	}
	return os.WriteFile(leaf.certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
}

// ensureJwtKeys keeps the pair on disk so tokens signed before a restart stay valid, even when the CA is regenerated
func ensureJwtKeys(p *pki) error {
	dir := filepath.Dir(p.JwtPrivateKeyFile)
	privatePem, privateErr := os.ReadFile(p.JwtPrivateKeyFile)
	publicPem, publicErr := os.ReadFile(p.JwtPublicKeyFile)
	if privateErr == nil && publicErr == nil {
		for _, key := range []struct {
			path    string
			content []byte
		}{{p.JwtPrivateKeyFile, privatePem}, {p.JwtPublicKeyFile, publicPem}} {
			block, _ := pem.Decode(key.content)
			if block == nil {
				return readError(key.path, errors.New("no pem block"), dir)
			}
		}
		p.JwtPrivateKeyPem, p.JwtPublicKeyPem = string(privatePem), string(publicPem)
		return nil
	}
	if privateErr != nil && !errors.Is(privateErr, fs.ErrNotExist) {
		return readError(p.JwtPrivateKeyFile, privateErr, dir)
	}
	if publicErr != nil && !errors.Is(publicErr, fs.ErrNotExist) {
		return readError(p.JwtPublicKeyFile, publicErr, dir)
	}

	var err error
	p.JwtPrivateKeyPem, p.JwtPublicKeyPem, err = newJwtKeyPair()
	if err != nil {
		return fmt.Errorf("jwt keys: %w", err)
	}
	err = os.WriteFile(p.JwtPrivateKeyFile, []byte(p.JwtPrivateKeyPem), 0600)
	if err != nil {
		return err
	}
	return os.WriteFile(p.JwtPublicKeyFile, []byte(p.JwtPublicKeyPem), 0600)
}

func newJwtKeyPair() (string, string, error) {
	jwtKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", err
	}
	privateDer, err := x509.MarshalPKCS8PrivateKey(jwtKey)
	if err != nil {
		return "", "", err
	}
	publicDer, err := x509.MarshalPKIXPublicKey(&jwtKey.PublicKey)
	if err != nil {
		return "", "", err
	}
	privatePem := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDer})
	publicPem := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDer})
	return string(privatePem), string(publicPem), nil
}
