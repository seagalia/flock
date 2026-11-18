package app

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/seagalia/flock/agent/f5/handlers"
	"github.com/seagalia/flock/agent/f5/reconciler"
	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/agentsdk/aggregaters"
	"github.com/seagalia/flock/sdk/agentsdk/certman"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

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
	cm, err := certman.New(caCertPool, a.cnf.TlsConfig.CertFile, a.cnf.TlsConfig.KeyFile, a.cnf.TlsConfig.CaFile)
	if err != nil {
		return fmt.Errorf("new certman: %w", err)
	}
	if err := cm.Watch(); err != nil {
		return fmt.Errorf("watch certman: %w", err)
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

// loadReconciler also makes the backend handler: it discovers the endpoints the reconciler reads, both call each other
// through changed
func (a *App) loadReconciler() {
	a.backends = handlers.NewBackendHandler(a.changed, a.rt.States)
	bigipConf := a.cnf.Bigip
	a.rec = reconciler.New(reconciler.Options{
		InstanceUUID:      a.cnf.ControllerConfig.InstanceUuid,
		DefaultPartition:  bigipConf.DefaultPartition,
		PartitionKey:      bigipConf.PartitionMetadataKey,
		Debounce:          time.Duration(bigipConf.Debounce),
		MaxDebounce:       time.Duration(bigipConf.MaxDebounce),
		MaxIncomplete:     time.Duration(bigipConf.MaxIncomplete),
		ResyncInterval:    time.Duration(bigipConf.ResyncInterval),
		GarbageAfterStart: time.Duration(bigipConf.GarbageAfterStart),
		DryRun:            bigipConf.DryRun,
	}, a.device, a.backends, a.rt.Store, a.rt.States, a.rt.SecretLinks)
}

func (a *App) changed() {
	a.rec.Changed()
}

func (a *App) loadAggregator() {
	// a clone: the client transport sets up HTTP/2 on its config while the agent server reads its own
	a.aggregator = aggregaters.NewAggregator(a.cnf.ControllerConfig.Addr, a.cnf.ControllerConfig.InstanceUuid, a.node, a.tlsConf.Clone(), a.rt.Waiter)
}

func (a *App) loadCertificateHandler() {
	certHandler := handlers.NewChangeHandler[*cert.Certificate]("f5_certificate", a.changed)
	adshandlers.NewAdsHandler[*cert.Certificate](a.rt, a.aggregator, adsChanCapacity, certHandler)
}

func (a *App) loadBackendHandler() {
	adshandlers.NewAdsHandler[*backend.Backend](a.rt, a.aggregator, adsChanCapacity, a.backends)
}

func (a *App) loadVsHandler() {
	vsHandler := handlers.NewChangeHandler[*vs.VirtualServer]("f5_virtual_server", a.changed)
	adshandlers.NewAdsHandler[*vs.VirtualServer](a.rt, a.aggregator, adsChanCapacity, vsHandler)
}

func (a *App) loadVipHandler() {
	vipHandler := handlers.NewChangeHandler[*vip.VIP]("f5_vip", a.changed)
	adshandlers.NewAdsHandler[*vip.VIP](a.rt, a.aggregator, adsChanCapacity, vipHandler)
}

// StartHandlers runs every ads handler and tells the reconciler it is synced once each resource type got its first
// response. The secret handler is not waited for: the controller sends no first response for secrets when no resource
// points at one.
func (a *App) StartHandlers(wg *sync.WaitGroup, hasError chan struct{}) {
	var waited []runtime.AdsHandler
	for _, h := range a.rt.Handlers("") {
		if h.TypeUrl() != helpers.TypeUrl[*secret.Secret]() {
			waited = append(waited, h)
		}
	}
	// a countdown in the callbacks instead of a goroutine waiting on them, which a stop before the sync would leave behind
	unsynced := &atomic.Int32{}
	unsynced.Store(int32(len(waited)))
	for _, h := range waited {
		h.SetOnSynced(func() {
			if unsynced.Add(-1) == 0 {
				a.rec.Synced()
			}
		})
	}
	for _, h := range a.rt.Handlers("") {
		a.runAdsHandler(wg, hasError, h)
	}
}

func (a *App) runAdsHandler(wg *sync.WaitGroup, hasError chan struct{}, h runtime.AdsHandler) {
	wg.Go(func() {
		err := h.Handle(a.ctx)
		if err != nil {
			a.entry.Error().Str("ads_handler_type", reflect.TypeOf(h).String()).Err(err).Msg("ads handler failed")
			reportFailure(hasError)
		}
	})
}

func (a *App) RunReconciler(wg *sync.WaitGroup) {
	wg.Go(func() {
		_ = a.rec.Run(a.ctx)
	})
}

func (a *App) loadSecretHandler() {
	adshandlers.NewAdsHandler[*secret.Secret](a.rt, a.aggregator, adsChanCapacity, adshandlers.NewSecretRefresher(a.rt))
}
