package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/domeneshop"
)

type DomeneshopParams struct {
	APIToken           string `mapstructure:"api_token"`
	APISecret          string `mapstructure:"api_secret"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *DomeneshopParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return domeneshop.NewDNSProviderConfig(cnf)
}

func (p *DomeneshopParams) ToConfig() (*domeneshop.Config, error) {
	cnf := domeneshop.NewDefaultConfig()
	setString(&cnf.APIToken, p.APIToken)
	setString(&cnf.APISecret, p.APISecret)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("domeneshop: %w", err)
	}
	return cnf, nil
}
