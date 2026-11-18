package factories

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"log/slog"
	"reflect"

	legocert "github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/challenge/dns01"
	"github.com/go-acme/lego/v5/lego"
	legolog "github.com/go-acme/lego/v5/log"
	"github.com/go-acme/lego/v5/registration"
	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/maps"
	slogzerolog "github.com/samber/slog-zerolog/v2"
	"github.com/seagalia/flock/provisioners/acme/params"
	"github.com/seagalia/flock/sdk/logging"
)

type providerMaker interface {
	ToProvider() (challenge.Provider, error)
}

type LegoFactory struct {
	DefaultCADirURL  string
	http01Enabled    bool
	tlsAlpn01Enabled bool
	autoRegister     bool
	dnsOptions       []dns01.ChallengeOption
	defaultMaker     providerMaker
}

func NewLegoFactory(defaultCADirURL string, defaultConfig map[string]any, http01Enabled, tlsAlpn01Enabled bool) (*LegoFactory, error) {
	entry := logging.Component("acme")
	logger := slog.New(slogzerolog.Option{Level: slogzerolog.ZeroLogLeveler{Logger: &entry}, Logger: &entry}.NewZerologHandler())
	legolog.SetDefault(logger)

	acmeParam := &params.AcmeParam{}
	err := mapstructure.Decode(defaultConfig, acmeParam)
	if err != nil {
		return nil, fmt.Errorf("failed to decode acme config: %w", err)
	}
	maker := findFirstProviderMaker(*acmeParam)
	return &LegoFactory{
		DefaultCADirURL:  defaultCADirURL,
		http01Enabled:    http01Enabled,
		tlsAlpn01Enabled: tlsAlpn01Enabled,
		autoRegister:     true,
		defaultMaker:     maker,
	}, nil
}

func (f *LegoFactory) SetAutoRegister(autoRegister bool) {
	f.autoRegister = autoRegister
}

// SetDns01SkipPropagationCheck makes dns-01 ask the CA to validate as soon as the DNS provider took the TXT record
func (f *LegoFactory) SetDns01SkipPropagationCheck(skip bool) {
	if !skip {
		f.dnsOptions = nil
		return
	}
	f.dnsOptions = []dns01.ChallengeOption{dns01.PropagationWait(0, true)}
}

func (f *LegoFactory) Obtain(
	ctx context.Context, req *params.CertRequest, request legocert.ObtainRequest, solver challenge.Provider,
) (*legocert.Resource, error) {
	client, err := f.MakeClient(ctx, req, solver)
	if err != nil {
		return nil, err
	}
	return client.Certificate.Obtain(ctx, request)
}

// challengeProviders gives the solver of the order to the challenges the provisioner serves, none for a nil solver.
func (f *LegoFactory) challengeProviders(solver challenge.Provider) (http01, tlsAlpn01 challenge.Provider) {
	if solver == nil {
		return nil, nil
	}
	if f.http01Enabled {
		http01 = solver
	}
	if f.tlsAlpn01Enabled {
		tlsAlpn01 = solver
	}
	return http01, tlsAlpn01
}

func (f *LegoFactory) MakeClient(
	ctx context.Context, req *params.CertRequest, solver challenge.Provider,
) (*lego.Client, error) {
	if req.KeyPem == nil {
		privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("failed to generate private key: %w", err)
		}
		req.KeyPem = &params.KeyPemRequest{Signer: privateKey}
	}

	caDirUrl := req.CADirURL
	if caDirUrl == "" {
		caDirUrl = f.DefaultCADirURL
	}

	config := lego.NewConfig(req)
	config.CADirURL = caDirUrl
	client, err := lego.NewClient(config)
	if err != nil {
		return nil, err
	}
	http01Provider, tlsAlpn01Provider := f.challengeProviders(solver)
	if http01Provider != nil {
		err := client.Challenge.SetHTTP01Provider(http01Provider)
		if err != nil {
			return nil, fmt.Errorf("failed to set HTTP 01 provider: %w", err)
		}
	}
	if tlsAlpn01Provider != nil {
		err := client.Challenge.SetTLSALPN01Provider(tlsAlpn01Provider)
		if err != nil {
			return nil, fmt.Errorf("failed to set TLS ALPN 01 provider: %w", err)
		}
	}

	dnsProvider, err := f.makeDnsProvider(req)
	if err != nil {
		return nil, err
	}
	if dnsProvider != nil {
		err = client.Challenge.SetDNS01Provider(dnsProvider, f.dnsOptions...)
		if err != nil {
			return nil, fmt.Errorf("failed to set DNS 01 provider: %w", err)
		}
	}

	if req.Registration == nil && f.autoRegister {
		reg, err := client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, fmt.Errorf("failed to register account: %w", err)
		}
		req.Registration = &params.RegistrationRequest{ExtendedAccount: reg}
	}

	return client, nil
}

func (f *LegoFactory) makeDnsProvider(req *params.CertRequest) (challenge.Provider, error) {
	nested := maps.Unflatten(req.DnsProvider, ".")
	flockMain := &params.FlockMainParam{}
	err := mapstructure.Decode(nested, flockMain)
	if err != nil {
		return nil, err
	}
	acmeParam := flockMain.Flock.AcmeParam
	maker := findFirstProviderMaker(acmeParam)
	if maker == nil {
		maker = f.defaultMaker
	}
	if maker == nil {
		return nil, nil
	}
	return maker.ToProvider()
}

func findFirstProviderMaker(acmeParams params.AcmeParam) providerMaker {
	rv := reflect.ValueOf(acmeParams)
	for _, field := range rv.Fields() {
		if field.Kind() != reflect.Pointer || field.IsNil() {
			continue
		}
		if field.Type().Implements(reflect.TypeFor[providerMaker]()) {
			return field.Interface().(providerMaker)
		}
	}
	return nil
}
