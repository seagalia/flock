package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/alwaysdata"
)

type AlwaysdataParams struct {
	APIKey             string `mapstructure:"api_key"`
	Account            string `mapstructure:"account"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *AlwaysdataParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return alwaysdata.NewDNSProviderConfig(cnf)
}

func (p *AlwaysdataParams) ToConfig() (*alwaysdata.Config, error) {
	cnf := alwaysdata.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.Account, p.Account)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("alwaysdata: %w", err)
	}
	return cnf, nil
}
