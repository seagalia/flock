package agentconfig

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/seagalia/flock/sdk/helpers"
)

// CommonName gives the CN of the first certificate of the PEM file certFile, the name of the agent. The controller
// identifies an agent by this name, so a CN no declared agent can have is refused here rather than at connection.
func CommonName(certFile string) (string, error) {
	content, err := os.ReadFile(certFile)
	if err != nil {
		return "", fmt.Errorf("read the agent certificate: %w", err)
	}
	// like tls.X509KeyPair, blocks of other types (a key bundled in the file) are skipped
	block, rest := pem.Decode(content)
	for block != nil && block.Type != "CERTIFICATE" {
		block, rest = pem.Decode(rest)
	}
	if block == nil {
		return "", fmt.Errorf("no PEM certificate found in %s", certFile)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse the agent certificate %s: %w", certFile, err)
	}
	commonName := cert.Subject.CommonName
	if !helpers.AgentNameRule.MatchString(commonName) {
		return "", fmt.Errorf("the common name %q of the agent certificate %s must match %s",
			commonName, certFile, helpers.AgentNamePattern)
	}
	return commonName, nil
}
