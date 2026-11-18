package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/metaname"
)

type MetanameParams struct {
	AccountReference   string `mapstructure:"account_reference"`
	APIKey             string `mapstructure:"api_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *MetanameParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return metaname.NewDNSProviderConfig(cnf)
}

func (p *MetanameParams) ToConfig() (*metaname.Config, error) {
	cnf := metaname.NewDefaultConfig()
	setString(&cnf.AccountReference, p.AccountReference)
	setString(&cnf.APIKey, p.APIKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("metaname: %w", err)
	}
	return cnf, nil
}
