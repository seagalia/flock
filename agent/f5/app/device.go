package app

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/seagalia/flock/agent/f5/bigip"
)

// loadDevice makes the BIG-IP client. The device is verified with ca_file, or the system roots without it.
func (a *App) loadDevice() error {
	bigipConf := a.cnf.Bigip
	tlsConf := &tls.Config{InsecureSkipVerify: bigipConf.InsecureSkipVerify} //nolint:gosec // an explicit choice of the config
	if bigipConf.CaFile != "" {
		caBytes, err := os.ReadFile(bigipConf.CaFile)
		if err != nil {
			return fmt.Errorf("read bigip ca_file: %w", err)
		}
		tlsConf.RootCAs = x509.NewCertPool()
		if !tlsConf.RootCAs.AppendCertsFromPEM(caBytes) {
			return fmt.Errorf("bigip ca_file %s holds no PEM certificate", bigipConf.CaFile)
		}
	}
	a.device = bigip.New(bigip.Options{
		URLs:           bigipConf.URLs,
		Username:       bigipConf.Username,
		Password:       bigipConf.Password,
		LoginProvider:  bigipConf.LoginProvider,
		TLSConfig:      tlsConf,
		RequestTimeout: time.Duration(bigipConf.RequestTimeout),
	})
	return nil
}
