package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/simply"
)

type SimplyParams struct {
	APIKey             string `mapstructure:"api_key"`
	AccountName        string `mapstructure:"account_name"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *SimplyParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return simply.NewDNSProviderConfig(cnf)
}

func (p *SimplyParams) ToConfig() (*simply.Config, error) {
	cnf := simply.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.AccountName, p.AccountName)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("simply: %w", err)
	}
	return cnf, nil
}
