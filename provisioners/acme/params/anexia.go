package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/anexia"
)

type AnexiaParams struct {
	Token              string `mapstructure:"token"`
	APIURL             string `mapstructure:"api_url"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *AnexiaParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return anexia.NewDNSProviderConfig(cnf)
}

func (p *AnexiaParams) ToConfig() (*anexia.Config, error) {
	cnf := anexia.NewDefaultConfig()
	setString(&cnf.Token, p.Token)
	setString(&cnf.APIURL, p.APIURL)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("anexia: %w", err)
	}
	return cnf, nil
}
