package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/namedotcom"
)

type NamedotcomParams struct {
	Username           string `mapstructure:"username"`
	APIToken           string `mapstructure:"api_token"`
	Server             string `mapstructure:"server"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *NamedotcomParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return namedotcom.NewDNSProviderConfig(cnf)
}

func (p *NamedotcomParams) ToConfig() (*namedotcom.Config, error) {
	cnf := namedotcom.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.APIToken, p.APIToken)
	setString(&cnf.Server, p.Server)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("namedotcom: %w", err)
	}
	return cnf, nil
}
