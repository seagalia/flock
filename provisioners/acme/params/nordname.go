package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/nordname"
)

type NordnameParams struct {
	APIKey             string `mapstructure:"api_key"`
	CustomerUUID       string `mapstructure:"customer_uuid"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *NordnameParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return nordname.NewDNSProviderConfig(cnf)
}

func (p *NordnameParams) ToConfig() (*nordname.Config, error) {
	cnf := nordname.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.CustomerUUID, p.CustomerUUID)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("nordname: %w", err)
	}
	return cnf, nil
}
