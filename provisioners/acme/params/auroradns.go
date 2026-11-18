package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/auroradns"
)

type AuroradnsParams struct {
	BaseURL            string `mapstructure:"base_url"`
	APIKey             string `mapstructure:"api_key"`
	Secret             string `mapstructure:"secret"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *AuroradnsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return auroradns.NewDNSProviderConfig(cnf)
}

func (p *AuroradnsParams) ToConfig() (*auroradns.Config, error) {
	cnf := auroradns.NewDefaultConfig()
	setString(&cnf.BaseURL, p.BaseURL)
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.Secret, p.Secret)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("auroradns: %w", err)
	}
	return cnf, nil
}
