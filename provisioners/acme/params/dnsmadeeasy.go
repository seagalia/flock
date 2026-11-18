package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/dnsmadeeasy"
)

type DnsmadeeasyParams struct {
	BaseURL            string `mapstructure:"base_url"`
	APIKey             string `mapstructure:"api_key"`
	APISecret          string `mapstructure:"api_secret"`
	Sandbox            string `mapstructure:"sandbox"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *DnsmadeeasyParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return dnsmadeeasy.NewDNSProviderConfig(cnf)
}

func (p *DnsmadeeasyParams) ToConfig() (*dnsmadeeasy.Config, error) {
	cnf := dnsmadeeasy.NewDefaultConfig()
	setString(&cnf.BaseURL, p.BaseURL)
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.APISecret, p.APISecret)

	err := errors.Join(
		setBool(&cnf.Sandbox, "sandbox", p.Sandbox),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("dnsmadeeasy: %w", err)
	}
	return cnf, nil
}
