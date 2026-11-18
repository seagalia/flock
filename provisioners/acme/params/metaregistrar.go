package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/metaregistrar"
)

type MetaregistrarParams struct {
	APIToken           string `mapstructure:"api_token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *MetaregistrarParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return metaregistrar.NewDNSProviderConfig(cnf)
}

func (p *MetaregistrarParams) ToConfig() (*metaregistrar.Config, error) {
	cnf := metaregistrar.NewDefaultConfig()
	setString(&cnf.APIToken, p.APIToken)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("metaregistrar: %w", err)
	}
	return cnf, nil
}
