package certmanagers

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	legocert "github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/provisioners/acme/challenges"
	"github.com/seagalia/flock/provisioners/acme/params"
	"github.com/seagalia/flock/sdk/agentsdk/leases"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
	"github.com/seagalia/flock/sdk/logging"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

const (
	defaultTickRenew   = 6 * time.Hour
	defaultTickCleanUp = 9 * time.Hour
	// an order waits for the DNS propagation (60s per domain) and polls the authorization for up to ~8 minutes
	obtainTimeout = 10 * time.Minute
	// each retry is a new order, a minute leaves a transient CA or DNS failure time to pass
	defaultRetryDelay    = 1 * time.Minute
	defaultRetryAttempts = 3
	pendingSize          = 100
	mdSecretRefKey       = "flock.acme.secret.ref"
	mdCertRefKey         = "flock.acme.cert.ref"
	mdAttachedKey        = "flock.acme.secret.attached"
	// defaultControllerCallTimeout bounds a unary call to the controller, which can accept a call and never answer
	defaultControllerCallTimeout = 30 * time.Second
	// writeBackTimeout bounds the write of an obtained certificate to the controller, which a stop doesn't cancel
	writeBackTimeout      = 30 * time.Second
	defaultRemovalKeptFor = time.Hour
	mdRetryAfterKey       = "flock.acme.retry_after"
	mdFailuresKey         = "flock.acme.failures"
	firstBackOff          = time.Hour
	maxBackOff            = 6 * time.Hour
	// the write of a certificate obtained is retried at the retry delay this many times in a row, then at the back-off
	writeFailuresBeforeBackOff = 3
)

// controllerCallTimeout is defaultControllerCallTimeout, a var only so specs don't wait the real bound
var controllerCallTimeout = defaultControllerCallTimeout

// removalKeptFor is defaultRemovalKeptFor, a var only so specs don't wait the real bound
var removalKeptFor = defaultRemovalKeptFor

//go:generate go tool mockgen -destination=mocks/mock_obtainer.go -package=mocks . Obtainer
//go:generate go tool mockgen -destination=mocks/mock_cert_client.go -package=mocks github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect CertificateClient
//go:generate go tool mockgen -destination=mocks/mock_secret_client.go -package=mocks github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect SecretClient

// Obtainer orders a certificate from the CA of req. It may fill req.KeyPem and req.Registration, the account the
// order made, so the caller can keep them.
type Obtainer interface {
	Obtain(
		ctx context.Context, req *params.CertRequest, request legocert.ObtainRequest, solver challenge.Provider,
	) (*legocert.Resource, error)
}

// AgentIdentity gives the uuid the controller knows the provisioner by, the aggregator learns it on the first stream
type AgentIdentity interface {
	// AgentUuid is empty until the controller gave the uuid
	AgentUuid() string
	WaitAgentUuid(ctx context.Context) (string, error)
}

type CertManager struct {
	entry        zerolog.Logger
	defaultEmail string
	identity     AgentIdentity
	obtainer     Obtainer
	certClient   svccertconnect.CertificateClient
	secretClient svcsecretconnect.SecretClient
	leases       *leases.Client
	// challenges is nil when neither http-01 nor tls-alpn-01 is enabled
	challenges *challenges.Table
	// states holds the obtain failures the status answers report
	states *stores.StateStore

	rwMutex     *sync.RWMutex
	certRecords map[string]*certRecord

	pending   chan queuedOrder
	certLocks *sync.Map

	// tasks holds every order, renewal and clean up in flight, Run waits for them before returning
	tasks          sync.WaitGroup
	firstRenewDone chan struct{}

	// runCtx is the ctx of Run, which the retries run on. The ctx of the order scheduling one ends with that order.
	runCtx      context.Context
	timersMutex sync.Mutex
	activities  map[string]*activity
	stopping    bool

	tickRenew     time.Duration
	tickCleanUp   time.Duration
	retryAttempts uint
	retryDelay    time.Duration
	leaseTTL      time.Duration
}

func (o *CertManager) SetTickRenew(tickRenew time.Duration) {
	o.tickRenew = tickRenew
}

func (o *CertManager) SetTickCleanUp(tickCleanUp time.Duration) {
	o.tickCleanUp = tickCleanUp
}

func (o *CertManager) SetRetry(attempts uint, delay time.Duration) {
	o.retryAttempts = attempts
	o.retryDelay = delay
}

func (o *CertManager) SetLeaseTTL(ttl time.Duration) {
	o.leaseTTL = ttl
}

func NewCertManager(
	obtainer Obtainer,
	certClient svccertconnect.CertificateClient,
	secretClient svcsecretconnect.SecretClient,
	leaseClient *leases.Client,
	identity AgentIdentity,
	states *stores.StateStore,
	defaultEmail string,
) *CertManager {
	return &CertManager{
		entry:          logging.Component("acme_cert_manager"),
		obtainer:       obtainer,
		certClient:     certClient,
		secretClient:   secretClient,
		leases:         leaseClient,
		activities:     map[string]*activity{},
		rwMutex:        &sync.RWMutex{},
		certRecords:    map[string]*certRecord{},
		pending:        make(chan queuedOrder, pendingSize),
		firstRenewDone: make(chan struct{}),
		certLocks:      &sync.Map{},
		identity:       identity,
		states:         states,
		tickRenew:      defaultTickRenew,
		tickCleanUp:    defaultTickCleanUp,
		retryAttempts:  defaultRetryAttempts,
		retryDelay:     defaultRetryDelay,
		leaseTTL:       leases.DefaultTTL,
		defaultEmail:   defaultEmail,
	}
}

func (o *CertManager) OnMessageAdded(ctx context.Context, message *cert.Certificate) error {
	o.entry.Debug().Msgf("certificate %s added/modified", message.GetUUID())
	o.forgetRemoval(message.GetUUID())
	// any replica records a certificate whoever provisioned it, so it renews it once due when the one that ordered it
	// is gone
	isAlreadyManaged := message.GetPrivateKey() != ""
	record, isRecorded := o.findRecord(message.GetUUID())
	domainsChanged := isRecorded && !sameDomains(message.GetDomainNames(), record.crt.GetDomainNames())
	if isAlreadyManaged && !domainsChanged {
		return o.storeInfoNoSecret(message, 0)
	}
	// a certificate with no chain, or whose chain misses its new domains, is recorded as due, so the renew tick orders
	// it again when its order is never written, by a replica that died holding its lease for instance. Every replica
	// records the new domains, so the one that didn't order them still renews for them.
	if message.GetCertChain() == "" || domainsChanged {
		err := o.storeInfoNoSecret(message, 1)
		if err != nil {
			return err
		}
	} else {
		o.addQueuedOrders(message.GetUUID(), 1)
	}

	order := queuedOrder{crt: proto.Clone(message).(*cert.Certificate), domainsChanged: domainsChanged}
	select {
	case o.pending <- order:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("queue certificate %s: %w", message.GetUUID(), ctx.Err())
	}
}

func (o *CertManager) OnMessageRemove(ctx context.Context, message *cert.Certificate) error {
	o.entry.Debug().Msgf("certificate %s removed", message.GetUUID())
	o.markRemoved(message.GetUUID())
	return o.remove(ctx, message)
}

func (o *CertManager) Name() string {
	return "acme"
}

func (o *CertManager) leafFromPEM(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM) // first block is the leaf, even with Bundle: true
	if block == nil {
		return nil, errors.New("invalid PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

// Run waits for the uuid of the provisioner before anything else, because every write to the controller carries it as
// the owner of the certificates and secrets. The obtains, renewals and clean ups run off the loop, so a slow CA or
// controller doesn't hold the queue. Run waits for them before returning, so an obtain finishing after the stop still
// writes its certificate back.
func (o *CertManager) Run(ctx context.Context) {
	o.runCtx = ctx
	agentUuid, isKnown := o.waitAgentUuid(ctx)
	if !isKnown {
		return
	}
	o.entry.Info().Str("agent_uuid", agentUuid).Msg("Starting acme cert manager ...")
	renewTicker := time.NewTicker(o.tickRenew)
	defer renewTicker.Stop()

	cleanUpTicker := time.NewTicker(o.tickCleanUp)
	defer cleanUpTicker.Stop()

	defer o.tasks.Wait()
	// a tick coming while the previous renewal or clean up still runs is skipped, the next one does the work
	renewing := &atomic.Bool{}
	cleaning := &atomic.Bool{}
	startAlone := func(running *atomic.Bool, work func(context.Context)) {
		if !running.CompareAndSwap(false, true) {
			return
		}
		o.tasks.Go(func() {
			defer running.Store(false)
			work(ctx)
		})
	}

	startAlone(renewing, func(ctx context.Context) {
		defer close(o.firstRenewDone)
		o.renewDue(ctx)
	})
	for {
		select {
		case <-renewTicker.C:
			startAlone(renewing, o.renewDue)
		case <-cleanUpTicker.C:
			startAlone(cleaning, o.cleanUpSecrets)
		case order := <-o.pending:
			o.tasks.Go(func() {
				o.obtainQueued(ctx, order)
			})
		case <-ctx.Done():
			o.entry.Info().Msg("Stopping acme cert manager, waiting for the obtains in flight ...")
			o.stopRetries()
			return
		}
	}
}

// waitAgentUuid tells false when ctx ends first, the controller may never accept this provisioner
func (o *CertManager) waitAgentUuid(ctx context.Context) (string, bool) {
	if agentUuid := o.identity.AgentUuid(); agentUuid != "" {
		return agentUuid, true
	}
	o.entry.Info().Msg("Waiting for the controller to give the uuid of this provisioner before provisioning")
	agentUuid, err := o.identity.WaitAgentUuid(ctx)
	if err != nil {
		return "", false
	}
	return agentUuid, true
}

func (o *CertManager) agentUuid() string {
	return o.identity.AgentUuid()
}

func (o *CertManager) renewDue(ctx context.Context) {
	var g errgroup.Group
	g.SetLimit(10)

	for _, record := range o.listDue() {
		g.Go(func() error {
			unlock := o.lockCert(record.crt.GetUUID())
			defer unlock()
			// the order queued may have written it while this waited for the lock
			current, isRecorded := o.findRecord(record.crt.GetUUID())
			if !isRecorded || !current.isDue() {
				return nil
			}
			o.runOrder(ctx, current.crt, false)
			return nil
		})
	}
	_ = g.Wait()
}
