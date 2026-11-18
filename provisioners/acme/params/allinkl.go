package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/allinkl"
)

type AllinklParams struct {
	Login              string `mapstructure:"login"`
	Password           string `mapstructure:"password"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *AllinklParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return allinkl.NewDNSProviderConfig(cnf)
}

func (p *AllinklParams) ToConfig() (*allinkl.Config, error) {
	cnf := allinkl.NewDefaultConfig()
	setString(&cnf.Login, p.Login)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("allinkl: %w", err)
	}
	return cnf, nil
}
