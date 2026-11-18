package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/checkdomain"
)

type CheckdomainParams struct {
	Endpoint           string `mapstructure:"endpoint"`
	Token              string `mapstructure:"token"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *CheckdomainParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return checkdomain.NewDNSProviderConfig(cnf)
}

func (p *CheckdomainParams) ToConfig() (*checkdomain.Config, error) {
	cnf := checkdomain.NewDefaultConfig()
	setString(&cnf.Token, p.Token)

	err := errors.Join(
		setURL(&cnf.Endpoint, "endpoint", p.Endpoint),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("checkdomain: %w", err)
	}
	return cnf, nil
}
