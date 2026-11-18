package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/porkbun"
)

type PorkbunParams struct {
	APIKey             string `mapstructure:"api_key"`
	SecretAPIKey       string `mapstructure:"secret_api_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *PorkbunParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return porkbun.NewDNSProviderConfig(cnf)
}

func (p *PorkbunParams) ToConfig() (*porkbun.Config, error) {
	cnf := porkbun.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.SecretAPIKey, p.SecretAPIKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("porkbun: %w", err)
	}
	return cnf, nil
}
