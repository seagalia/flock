package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"reflect"
	"sync"

	"github.com/seagalia/flock/provisioners/acme/challenges"
	"github.com/seagalia/flock/provisioners/config"
	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/agentsdk/aggregaters"
	"github.com/seagalia/flock/sdk/agentsdk/certman"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
)

func (a *App) loadCertificateHandler() {
	if a.certManager == nil {
		return
	}
	adshandlers.NewAdsHandler[*cert.Certificate](a.rt, a.aggregator, DefaultBufCapAdsHdl, a.certManager).
		WithFilters(config.MakeProvisionerFilters(config.ACMEProvisionerName))
}

func (a *App) loadSecretHandler() {
	if a.certManager == nil {
		return
	}
	messageHandlers := []adshandlers.MessageHandler[*secret.Secret]{adshandlers.NewSecretRefresher(a.rt)}
	if a.challengeTable != nil {
		messageHandlers = append(messageHandlers, challenges.NewSecretHandler(a.challengeTable))
	}
	adshandlers.NewAdsHandler[*secret.Secret](a.rt, a.aggregator, DefaultBufCapAdsHdl, messageHandlers...)
}

func (a *App) makeCertman(caCertPool *x509.CertPool, certFile, keyFile, caFile string) (*certman.CertMan, error) {
	cm, err := certman.New(caCertPool, certFile, keyFile, caFile)
	if err != nil {
		return nil, fmt.Errorf("new certman: %w", err)
	}
	if err := cm.Watch(); err != nil {
		return nil, fmt.Errorf("watch certman: %w", err)
	}
	return cm, nil
}

func (a *App) loadTlsConfig() error {
	var caCertPool *x509.CertPool
	if a.cnf.TlsConfig.CaFile != "" {
		caBytes, err := os.ReadFile(a.cnf.TlsConfig.CaFile)
		if err != nil {
			return fmt.Errorf("read ca file: %w", err)
		}
		caCertPool = x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caBytes) {
			return fmt.Errorf("ca file %s holds no PEM certificate", a.cnf.TlsConfig.CaFile)
		}
	}

	cm, err := a.makeCertman(caCertPool, a.cnf.TlsConfig.CertFile, a.cnf.TlsConfig.KeyFile, a.cnf.TlsConfig.CaFile)
	if err != nil {
		return fmt.Errorf("make certman: %w", err)
	}
	a.certman = cm
	a.tlsConf = &tls.Config{
		ServerName:           a.cnf.ControllerConfig.ServerName,
		GetCertificate:       cm.GetCertificate,
		GetClientCertificate: cm.GetClientCertificate,
		RootCAs:              caCertPool,
		ClientCAs:            caCertPool,
		ClientAuth:           tls.VerifyClientCertIfGiven,
	}
	return nil
}

func (a *App) loadAggregator() {
	a.aggregator = aggregaters.NewAggregator(a.cnf.ControllerConfig.Addr, a.cnf.ControllerConfig.InstanceUuid, a.node, a.tlsConf.Clone(), a.rt.Waiter)
}

func (a *App) StartHandlers(wg *sync.WaitGroup, hasError chan struct{}) {
	adsHandlers := a.rt.Handlers("")
	wg.Add(len(adsHandlers))
	for _, h := range adsHandlers {
		go func(wg *sync.WaitGroup, ctx context.Context, adsHandler runtime.AdsHandler, hasError chan struct{}) {
			defer wg.Done()
			err := adsHandler.Handle(ctx)
			if err != nil {
				a.entry.Error().
					Str("ads_handler_type", reflect.TypeOf(adsHandler).String()).
					Msgf("handle ads : %s", err)
				reportFailure(hasError)
			}
			a.entry.Debug().Str("ads_handler_type", reflect.TypeOf(adsHandler).String()).Msg("ads handler stopped")
		}(wg, a.ctx, h, hasError)
	}
}
