package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/fornex"
)

type FornexParams struct {
	APIKey             string `mapstructure:"api_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *FornexParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return fornex.NewDNSProviderConfig(cnf)
}

func (p *FornexParams) ToConfig() (*fornex.Config, error) {
	cnf := fornex.NewDefaultConfig()
	setString(&cnf.Example, p.APIKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("fornex: %w", err)
	}
	return cnf, nil
}
