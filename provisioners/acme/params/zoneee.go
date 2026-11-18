package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/zoneee"
)

type ZoneeeParams struct {
	Endpoint           string `mapstructure:"endpoint"`
	Username           string `mapstructure:"username"`
	APIKey             string `mapstructure:"api_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *ZoneeeParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return zoneee.NewDNSProviderConfig(cnf)
}

func (p *ZoneeeParams) ToConfig() (*zoneee.Config, error) {
	cnf := zoneee.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.APIKey, p.APIKey)

	err := errors.Join(
		setURL(&cnf.Endpoint, "endpoint", p.Endpoint),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("zoneee: %w", err)
	}
	return cnf, nil
}
