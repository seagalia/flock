package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/lightsail"
)

type LightsailParams struct {
	DNSZone            string `mapstructure:"dns_zone"`
	Region             string `mapstructure:"region"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *LightsailParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return lightsail.NewDNSProviderConfig(cnf)
}

func (p *LightsailParams) ToConfig() (*lightsail.Config, error) {
	cnf := lightsail.NewDefaultConfig()
	setString(&cnf.DNSZone, p.DNSZone)
	setString(&cnf.Region, p.Region)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("lightsail: %w", err)
	}
	return cnf, nil
}
