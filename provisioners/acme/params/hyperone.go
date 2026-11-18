package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/hyperone"
)

type HyperoneParams struct {
	APIEndpoint        string `mapstructure:"api_endpoint"`
	LocationID         string `mapstructure:"location_id"`
	PassportLocation   string `mapstructure:"passport_location"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *HyperoneParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return hyperone.NewDNSProviderConfig(cnf)
}

func (p *HyperoneParams) ToConfig() (*hyperone.Config, error) {
	cnf := hyperone.NewDefaultConfig()
	setString(&cnf.APIEndpoint, p.APIEndpoint)
	setString(&cnf.LocationID, p.LocationID)
	setString(&cnf.PassportLocation, p.PassportLocation)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("hyperone: %w", err)
	}
	return cnf, nil
}
