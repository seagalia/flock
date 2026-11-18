package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"reflect"
	"sync"

	"github.com/seagalia/flock/agent/starling/backendctl"
	"github.com/seagalia/flock/agent/starling/handlers"
	wafclient "github.com/seagalia/flock/agent/starling/waf/client"
	"github.com/seagalia/flock/sdk/agentsdk/adshandlers"
	"github.com/seagalia/flock/sdk/agentsdk/aggregaters"
	"github.com/seagalia/flock/sdk/agentsdk/certman"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
)

func (a *App) loadVipHandler() {
	hds := []adshandlers.MessageHandler[*vip.VIP]{
		handlers.NewVipHandler(a.netman, a.adHandler, a.rt.Store.VIPs()),
	}

	if a.cnf.DevMode {
		var logHandler adshandlers.MessageHandler[*vip.VIP]
		logHandler = adshandlers.NewLogHandler[*vip.VIP]()
		if a.vipPlacer != nil {
			logHandler = handlers.NewDynamicPlacementVipHandler(a.vipPlacer, logHandler, a.rt.Store.VIPs())
		}
		hds = append(hds, logHandler)
	}

	if a.cnf.AdvertisementConfig.L2Config != nil {
		var l2Handler adshandlers.MessageHandler[*vip.VIP]
		l2Handler = handlers.NewLayer2Handler(
			a.name,
			a.cnf.AdvertisementConfig.Interfaces,
			a.cnf.AdvertisementConfig.L2Config.AllInterfaces,
			a.l2Announcer,
		)
		if a.cnf.AdvertisementConfig.L2Config.UseDynamicPlacement {
			a.entry.Debug().Msg("use dynamic placement for l2")
			a.vipPlacer.AddPlaceOn(starling_announce.StarlingAnnounce_LAYER2)
			l2Handler = handlers.NewDynamicPlacementVipHandler(a.vipPlacer, l2Handler, a.rt.Store.VIPs())
		}
		hds = append(hds, l2Handler)
	}

	if a.cnf.AdvertisementConfig.BgpConfig != nil {
		var bgpHandler adshandlers.MessageHandler[*vip.VIP]
		bgpHandler = handlers.NewBgpHandler(a.bgpServer)
		if a.cnf.AdvertisementConfig.BgpConfig.UseDynamicPlacement {
			a.entry.Debug().Msg("use dynamic placement for bgp")
			a.vipPlacer.AddPlaceOn(starling_announce.StarlingAnnounce_BGP)
			bgpHandler = handlers.NewDynamicPlacementVipHandler(a.vipPlacer, bgpHandler, a.rt.Store.VIPs())
		}
		hds = append(hds, bgpHandler)
	}
	hds = append(hds, a.rt.IpPorts)

	adshandlers.NewAdsHandler[*vip.VIP](a.rt, a.aggregator, DefaultBufCapAdsHdl, hds...)
}

func (a *App) loadJointer() {
	a.jointer = backendctl.NewJoint(a.ctx, a.rt.States)
}

func (a *App) loadVsHandler() error {
	// ipvs is faked in dev mode, envoy is the only one able to carry tcp and udp traffic there
	envoyOnlyL4 := a.cnf.DevMode && a.runEnvoy
	tcpRouteHandler := handlers.NewTcpRouteHandler(a.jointer, a.configWatcher, a.netman, envoyOnlyL4, a.rt.Store, a.rt.States)
	udpRouteHandler := handlers.NewUdpRouteHandler(a.jointer, a.configWatcher, a.netman, envoyOnlyL4, a.rt.Store, a.rt.States)
	wafClient, err := wafclient.NewClient(a.cnf.WafConfig.SocketPath())
	if err != nil {
		return err
	}
	httpRouteHandler := handlers.NewHttpRouteHandler(a.jointer, a.configWatcher, wafClient, a.rt.Store)
	tlsTcpRouteHandlers := handlers.NewTlsTcpRouteHandler(a.jointer, a.configWatcher, a.rt.Store)

	a.garbageCollected = append(a.garbageCollected, tcpRouteHandler, udpRouteHandler)

	vsHandler := handlers.NewVsHandler(
		a.netman,
		tcpRouteHandler,
		udpRouteHandler,
		httpRouteHandler,
		tlsTcpRouteHandlers,
		a.rt.Store,
	)
	a.adHandler = vsHandler

	adshandlers.NewAdsHandler[*vs.VirtualServer](a.rt, a.aggregator, DefaultBufCapAdsHdl, vsHandler)
	return nil
}

func (a *App) loadBackendHandler() {
	backendHandler := handlers.NewBackendHandler(a.jointer)
	adshandlers.NewAdsHandler[*backend.Backend](a.rt, a.aggregator, DefaultBufCapAdsHdl, backendHandler)
}

func (a *App) loadCertificateHandler() {
	certHandler := handlers.NewCertificateHandler(a.configWatcher)
	adshandlers.NewAdsHandler[*cert.Certificate](a.rt, a.aggregator, DefaultBufCapAdsHdl, certHandler)
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
		caCertPool = x509.NewCertPool()
		caBytes, err := os.ReadFile(a.cnf.TlsConfig.CaFile)
		if err != nil {
			return fmt.Errorf("read ca file: %w", err)
		}
		if ok := caCertPool.AppendCertsFromPEM(caBytes); !ok {
			return fmt.Errorf("append ca file: no certificate found in %s", a.cnf.TlsConfig.CaFile)
		}
	}

	cm, err := a.makeCertman(caCertPool, a.cnf.TlsConfig.CertFile, a.cnf.TlsConfig.KeyFile, a.cnf.TlsConfig.CaFile)
	if err != nil {
		return fmt.Errorf("make certman: %w", err)
	}
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
	a.aggregator = aggregaters.NewAggregator(a.cnf.ControllerConfig.Addr, a.cnf.ControllerConfig.InstanceUuid, a.node, a.tlsConf, a.rt.Waiter)
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

// loadSecretHandler subscribes to the secrets the resources point at, a secret change applies them again.
func (a *App) loadSecretHandler() {
	adshandlers.NewAdsHandler[*secret.Secret](a.rt, a.aggregator, DefaultBufCapAdsHdl, adshandlers.NewSecretRefresher(a.rt))
}
