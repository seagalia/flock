package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/njalla"
)

type NjallaParams struct {
	Token              string `mapstructure:"token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *NjallaParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return njalla.NewDNSProviderConfig(cnf)
}

func (p *NjallaParams) ToConfig() (*njalla.Config, error) {
	cnf := njalla.NewDefaultConfig()
	setString(&cnf.Token, p.Token)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("njalla: %w", err)
	}
	return cnf, nil
}
