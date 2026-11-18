package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/netcup"
)

type NetcupParams struct {
	Key                string `mapstructure:"key"`
	Password           string `mapstructure:"password"`
	Customer           string `mapstructure:"customer"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *NetcupParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return netcup.NewDNSProviderConfig(cnf)
}

func (p *NetcupParams) ToConfig() (*netcup.Config, error) {
	cnf := netcup.NewDefaultConfig()
	setString(&cnf.Key, p.Key)
	setString(&cnf.Password, p.Password)
	setString(&cnf.Customer, p.Customer)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("netcup: %w", err)
	}
	return cnf, nil
}
