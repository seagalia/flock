package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/digitalocean"
)

type DigitaloceanParams struct {
	BaseURL            string `mapstructure:"base_url"`
	AuthToken          string `mapstructure:"auth_token"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *DigitaloceanParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return digitalocean.NewDNSProviderConfig(cnf)
}

func (p *DigitaloceanParams) ToConfig() (*digitalocean.Config, error) {
	cnf := digitalocean.NewDefaultConfig()
	setString(&cnf.BaseURL, p.BaseURL)
	setString(&cnf.AuthToken, p.AuthToken)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("digitalocean: %w", err)
	}
	return cnf, nil
}
