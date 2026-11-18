package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/exoscale"
)

type ExoscaleParams struct {
	APIKey             string `mapstructure:"api_key"`
	APISecret          string `mapstructure:"api_secret"`
	Endpoint           string `mapstructure:"endpoint"`
	HTTPTimeout        string `mapstructure:"http_timeout"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *ExoscaleParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return exoscale.NewDNSProviderConfig(cnf)
}

func (p *ExoscaleParams) ToConfig() (*exoscale.Config, error) {
	cnf := exoscale.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.APISecret, p.APISecret)
	setString(&cnf.Endpoint, p.Endpoint)

	err := errors.Join(
		setDuration(&cnf.HTTPTimeout, "http_timeout", p.HTTPTimeout),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("exoscale: %w", err)
	}
	return cnf, nil
}
