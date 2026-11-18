// Improvement from code https://github.com/dyson/certman made by Dyson Simmons to allow update CA
// and implement GetClientCertificate for mtls

// Package certman reloads the certificate, key and CA files of a tls config when they change,
// CAs are appended to the cert pool.
package certman

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/logging"

	"github.com/fsnotify/fsnotify"
)

// A CertMan represents a certificate manager able to watch certificate
// and key pairs for changes.
type CertMan struct {
	mu       sync.RWMutex
	certFile string
	keyFile  string
	caFile   string
	certPool *x509.CertPool
	keyPair  *tls.Certificate
	watcher  *fsnotify.Watcher
	watching chan bool
	entry    zerolog.Logger
}

// New creates a CertMan watching certFile, keyFile and caFile, relative paths are accepted.
func New(certPool *x509.CertPool, certFile, keyFile, caFile string) (*CertMan, error) {
	var err error

	certFile, err = filepath.Abs(certFile)
	if err != nil {
		return nil, err
	}

	keyFile, err = filepath.Abs(keyFile)
	if err != nil {
		return nil, err
	}

	cm := &CertMan{
		mu:       sync.RWMutex{},
		certFile: certFile,
		keyFile:  keyFile,
		caFile:   caFile,
		certPool: certPool,
		entry:    logging.Component("certman"),
	}

	return cm, nil
}

// Watch reloads the files when they change, a failed load keeps the previous certificates.
func (cm *CertMan) Watch() error {
	var err error

	if cm.watcher, err = fsnotify.NewWatcher(); err != nil {
		return fmt.Errorf("can't create watcher: %w", err)
	}

	if err = cm.watcher.Add(cm.certFile); err != nil {
		return fmt.Errorf("can't watch cert file: %w", err)
	}

	if err = cm.watcher.Add(cm.keyFile); err != nil {
		return fmt.Errorf("can't watch key file: %w", err)
	}

	if err = cm.watcher.Add(cm.caFile); err != nil {
		return fmt.Errorf("can't watch ca file: %w", err)
	}

	if err := cm.loadPair(); err != nil {
		cm.entry.Error().Msgf("can't load cert or key file: %v", err)
	}

	cm.entry.Info().Msg("watching for cert/key or ca change")

	cm.watching = make(chan bool)

	go cm.run()

	return nil
}

func (cm *CertMan) loadPair() error {
	keyPair, err := tls.LoadX509KeyPair(cm.certFile, cm.keyFile)
	if err == nil {
		cm.mu.Lock()
		cm.keyPair = &keyPair
		cm.mu.Unlock()
		cm.entry.Info().Msg("certificate and key loaded")
	}

	return err
}

func (cm *CertMan) loadCA() error {
	if cm.certPool == nil {
		return nil
	}
	b, err := os.ReadFile(cm.caFile)
	if err != nil {
		return fmt.Errorf("can't read ca file: %w", err)
	}
	cm.mu.Lock()
	added := cm.certPool.AppendCertsFromPEM(b)
	cm.mu.Unlock()
	if !added {
		return fmt.Errorf("can't append ca to cert pool")
	}
	cm.entry.Info().Msg("ca appended to cert pool")
	return err
}

func (cm *CertMan) run() {
loop:
	for {
		select {
		case <-cm.watching:
			break loop
		case event := <-cm.watcher.Events:
			cm.entry.Debug().Msgf("watch event: %v", event)
			if event.Name == cm.caFile {
				if err := cm.loadCA(); err != nil {
					cm.entry.Error().Msgf("can't load ca file: %v", err)
				}
				continue
			}
			if err := cm.loadPair(); err != nil {
				cm.entry.Error().Msgf("can't load cert or key file: %v", err)
			}
		case err := <-cm.watcher.Errors:
			cm.entry.Error().Msgf("error watching files: %v", err)
		}
	}

	cm.entry.Info().Msg("stopped watching")

	cm.watcher.Close()
}

// GetCertificate gives the loaded certificate, for tls.Config.GetCertificate.
func (cm *CertMan) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return cm.keyPair, nil
}

func (cm *CertMan) GetClientCertificate(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return cm.keyPair, nil
}

// Stop stops watching the files.
func (cm *CertMan) Stop() {
	cm.watching <- false
}
