package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"github.com/seagalia/flock/sdk/metrics"
	"net/http"
	"os"
	"reflect"
	"time"

	"github.com/ArthurHlt/emitter"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/controller/dbs"
	"github.com/seagalia/flock/controller/dbstore"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/controller/ipams"
	"github.com/seagalia/flock/controller/lockers"
	"github.com/seagalia/flock/controller/multi"
	"github.com/seagalia/flock/controller/permissioners"
	"github.com/seagalia/flock/controller/proxmetrics"
	"github.com/seagalia/flock/sdk/flock/api/observer/v1"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/observe"
)

type Closeable interface {
	Close() error
}

type Runnable interface {
	Run(ctx context.Context) error
}

type App struct {
	entry          zerolog.Logger
	cnf            *config.Config
	storeManager   *dbstore.DbStoreManager
	dbMan          *dbs.DbManager
	ctx            context.Context
	cancelFunc     context.CancelFunc
	mtlsConf       *tls.Config
	instantiater   instantiaters.Instantiater
	ipam           ipams.Ipam
	metricsFetcher *proxmetrics.Fetcher
	permissioner   permissioners.Permissioner
	multiRunnable  Runnable
	nats           *multi.Nats
	locker         lockers.Locker
	leases         *lockers.Leases
	// testRoutes lets the specs mount handlers on the real mux, to hold a call in flight during a stop; it stays nil
	// in production
	testRoutes func(mux *http.ServeMux)
}

func NewApp(cnf *config.Config) (*App, error) {
	metrics.EnableAppMetrics(config.AppName)
	ctx, cancelFunc := context.WithCancel(context.Background())
	app := &App{
		entry:      logging.Component("app"),
		cnf:        cnf,
		ctx:        ctx,
		cancelFunc: cancelFunc,
	}

	err := app.load()
	if err != nil {
		// RunServer, which releases them on a stop, will never run
		cancelFunc()
		if app.dbMan != nil {
			err = errors.Join(err, app.dbMan.Close())
		}
		return nil, err
	}
	return app, nil
}

func (a *App) load() error {
	err := a.preInit()
	if err != nil {
		return fmt.Errorf("app preInit: %w", err)
	}

	err = a.loadMTlsConfig()
	if err != nil {
		return fmt.Errorf("app loadMTlsConfig: %w", err)
	}

	err = a.loadStorerManager()
	if err != nil {
		return fmt.Errorf("app loadStorerManager: %w", err)
	}

	a.loadPermissioner()

	err = a.loadInstantiater(a.ctx)
	if err != nil {
		return fmt.Errorf("app loadInstantiater: %w", err)
	}

	err = a.loadIpam(a.ctx)
	if err != nil {
		return fmt.Errorf("app loadIpam: %w", err)
	}

	err = a.loadLocker(a.ctx)
	if err != nil {
		return fmt.Errorf("app loadLocker: %w", err)
	}

	err = a.loadLeases(a.ctx)
	if err != nil {
		return fmt.Errorf("app loadLeases: %w", err)
	}

	err = a.loadMultiRunnable()
	if err != nil {
		return fmt.Errorf("app loadMultiRunnable: %w", err)
	}

	a.loadProxmetricsFetcher()

	return nil
}

func (a *App) loadMultiRunnable() error {
	if a.cnf.Multi == nil {
		return nil
	}
	if a.cnf.Multi.Nats == nil {
		return nil
	}
	var err error
	natsMulti, err := multi.NewNats(a.cnf.Multi.Nats)
	if err != nil {
		return fmt.Errorf("could not create multi nats: %w", err)
	}
	a.multiRunnable = natsMulti
	a.nats = natsMulti
	return nil
}

func (a *App) loadMTlsConfig() error {
	cert, err := tls.LoadX509KeyPair(a.cnf.Mtls.CertFile, a.cnf.Mtls.KeyFile)
	if err != nil {
		return fmt.Errorf("load x509 key pair: %w", err)
	}
	var caCertPool *x509.CertPool
	if a.cnf.Mtls.CaFile != "" {
		caCertPool = x509.NewCertPool()
		caBytes, err := os.ReadFile(a.cnf.Mtls.CaFile)
		if err != nil {
			return fmt.Errorf("read ca file: %w", err)
		}
		if ok := caCertPool.AppendCertsFromPEM(caBytes); !ok {
			return fmt.Errorf("append ca file: no certificate found in %s", a.cnf.Mtls.CaFile)
		}
	}

	a.mtlsConf = &tls.Config{
		ServerName:   a.cnf.Mtls.ServerName,
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		ClientAuth:   tls.VerifyClientCertIfGiven,
	}
	return nil
}

func (a *App) loadProxmetricsFetcher() {
	scrapper := proxmetrics.NewScraper(a.mtlsConf)
	a.metricsFetcher = proxmetrics.NewFetcher(scrapper, a.instantiater, a.storeManager.Instances().(dbstore.LightStorer))
}

func (a *App) loadInstantiater(ctx context.Context) error {
	var err error
	if len(a.cnf.Instantiators) == 0 {
		a.instantiater = instantiaters.NewNull()
		a.entry.Warn().Msg("no instantiator is configured, no agent is declared so none can connect")
	} else {
		a.instantiater, err = instantiaters.NewDbMulti(ctx, a.dbMan, a.cnf.Instantiators)
		if err != nil {
			return fmt.Errorf("could not create instantiater: %w", err)
		}
	}
	a.entry.Debug().Stringer("instantiater", reflect.TypeOf(a.instantiater)).Msg("instantiater loaded")
	return nil
}

func (a *App) loadIpam(ctx context.Context) error {
	ipamConf := a.cnf.IPAM
	if ipamConf.Disabled {
		return nil
	}

	var err error
	if ipamConf.Remote != nil {
		a.ipam, err = ipams.NewRemote(ipamConf.Remote.URL, ipamConf.Remote.InsecureSkipVerify, ipamConf.Remote.Headers)
		if err != nil {
			return fmt.Errorf("could not create remote ipam: %w", err)
		}
		a.entry.Debug().Msg("remote ipam loaded")
		return nil
	}

	a.ipam, err = ipams.NewDb(ctx, a.dbMan)
	if err != nil {
		return fmt.Errorf("could not create ipam: %w", err)
	}
	a.entry.Debug().Msg("internal ipam loaded")
	return nil
}

func (a *App) loadLocker(ctx context.Context) error {
	var err error
	a.locker, err = lockers.NewDb(ctx, a.dbMan, lockers.DefaultTTL)
	if err != nil {
		return fmt.Errorf("could not create db locker: %w", err)
	}
	return nil
}

func (a *App) loadLeases(ctx context.Context) error {
	var err error
	a.leases, err = lockers.NewLeases(ctx, a.dbMan)
	if err != nil {
		return fmt.Errorf("could not create leases: %w", err)
	}
	return nil
}

func (a *App) preInit() error {
	err := logging.Setup(logging.Options{
		Level:            a.cnf.Log.Level,
		NoColor:          a.cnf.Log.NoColor,
		ForceColors:      a.cnf.Log.ForceColors,
		InJson:           a.cnf.Log.InJson,
		FilterComponents: a.cnf.Log.FilterComponents,
	})
	if err != nil {
		return fmt.Errorf("setup logging: %w", err)
	}
	a.logEvents()
	return nil
}

func (a *App) logEvents() {
	if zerolog.GlobalLevel() > zerolog.DebugLevel {
		return
	}
	observe.OnAll(observe.ListenerFuncSubject(func(event *emitter.EventOf[*observer.Subject]) {
		subj := event.TypedSubject()
		log.Debug().
			Str("component", "observe").
			Str("type_url", subj.GetTypeUrl()).
			Str("identifier", subj.GetIdentifier()).
			Str("signature", subj.GetSignature()).
			Interface("metadata", subj.GetMetadata().AsMap()).
			Msgf("event '%s'", event.Topic())

	}))
}

func (a *App) Config() *config.Config {
	return a.cnf
}

func (a *App) loadStorerManager() error {
	// the database lives until dbMan.Close, after the servers have stopped, because a.ctx is cancelled before them
	// and calls in flight still need it
	dbCtx := context.Background()
	var err error
	a.dbMan, err = dbs.NewDbManager(dbCtx,
		dbs.DbType(a.cnf.StoreManager.SqlStorer.DbType),
		a.cnf.StoreManager.SqlStorer.DSN,
		time.Duration(a.cnf.StoreManager.SqlStorer.CnxMaxLifetime),
		time.Duration(a.cnf.StoreManager.SqlStorer.CnxMaxIdleTime),
		a.cnf.StoreManager.SqlStorer.CnxMaxOpen,
		a.cnf.StoreManager.SqlStorer.CnxMaxIdle,
	)
	if err != nil {
		return fmt.Errorf("failed load sql db manager: %w", err)
	}
	a.storeManager, err = dbstore.NewDbStoreManager(
		dbCtx,
		a.dbMan,
		a.cnf.StoreManager.SqlStorer.Secret,
	)
	if err != nil {
		return fmt.Errorf("sql store manager: %w", err)
	}
	return nil
}
